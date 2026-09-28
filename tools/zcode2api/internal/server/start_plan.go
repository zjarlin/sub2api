package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"glm-zcode-2api/internal/config"
	"glm-zcode-2api/internal/credential"
	"glm-zcode-2api/internal/upstream"
	"sub2api/builtinlogin"
)

func (s *Server) buildStartPlanCredential(ctx context.Context, data map[string]any, provider string) (credential.Credential, error) {
	user, _ := data["user"].(map[string]any)
	origin := strings.TrimRight(s.cfg.Upstream.GatewayOrigin, "/")
	if origin == "" {
		origin = "https://zcode.z.ai"
	}
	token := strings.TrimSpace(stringVal(data, "token"))
	hadBearerPrefix := strings.HasPrefix(strings.ToLower(token), "bearer ")
	if hadBearerPrefix {
		token = strings.TrimSpace(token[len("bearer "):])
	}
	s.logger.Printf("event=zcode_login stage=credential token_bearer_prefix=%t", hadBearerPrefix)
	cred := credential.Credential{
		APIKey:     token,
		BaseURL:    origin + "/api/v1/zcode-plan/anthropic",
		ProviderID: "account:" + provider + "-start-plan",
		Provider:   firstNonEmpty(stringVal(user, "name"), stringVal(user, "display_name"), "ZCode Start Plan"),
		Source:     "builtin-login",
		Plan:       credential.PlanStart,
	}
	models, err := s.startPlanModels(ctx, cred)
	if err != nil {
		s.logger.Printf("event=zcode_login stage=entitlement outcome=failed error_type=%T", err)
		return credential.Credential{}, &builtinlogin.PublicError{Status: http.StatusBadGateway, Message: "Unable to verify Start Plan login and entitlements; retry authorization"}
	}
	s.logger.Printf("event=zcode_login stage=entitlement outcome=accepted model_count=%d", len(models))
	if len(models) == 0 {
		return credential.Credential{}, &builtinlogin.PublicError{Status: http.StatusForbidden, Message: "This account has no active Start Plan entitlement for the configured models"}
	}
	return cred, nil
}

func effectivePlan(cred credential.Credential) string {
	if cred.Plan == "" {
		return credential.PlanCoding
	}
	return cred.Plan
}

// 仅在 JWT 明确声明已过期时本地拒绝；其余登录凭据由上游校验。
func checkStartPlanToken(token string) error {
	parts := strings.Split(token, ".")
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	if len(parts) == 3 {
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err == nil && json.Unmarshal(payload, &claims) == nil && claims.ExpiresAt > 0 && claims.ExpiresAt <= time.Now().Unix() {
			return &upstream.Error{Status: http.StatusUnauthorized, Type: "start_plan_reauthorization_required", Message: "Start Plan login is invalid or expired; sign in again"}
		}
	}
	if strings.TrimSpace(token) == "" {
		return &upstream.Error{Status: http.StatusUnauthorized, Type: "start_plan_reauthorization_required", Message: "Start Plan login is invalid or expired; sign in again"}
	}
	return nil
}

// 每次请求只取一次新验证参数，参数不落盘，也不从调用方请求头导入。
func (s *Server) prepareStartPlanRequest(ctx context.Context, cred credential.Credential, client *upstream.Client) error {
	if err := checkStartPlanToken(cred.APIKey); err != nil {
		return err
	}
	cfg := s.cfg.Upstream
	if cfg.StartPlanVerifierURL == "" || cfg.StartPlanVerifierKey == "" {
		return &upstream.Error{Status: http.StatusServiceUnavailable, Type: "start_plan_verifier_unavailable", Message: "Configure the Start Plan browser verification service before calling this plan"}
	}
	body, err := json.Marshal(map[string]any{"source_headers": client.Headers})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 125*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.StartPlanVerifierURL, "/")+"/verify", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.StartPlanVerifierKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := s.verifierHTTP.Do(req)
	if err != nil {
		return &upstream.Error{Status: http.StatusBadGateway, Type: "start_plan_verifier_unavailable", Message: "Start Plan browser verification service is unreachable or timed out"}
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusConflict {
		return &upstream.Error{Status: http.StatusConflict, Type: "start_plan_interactive_verification_required", Message: "Complete Start Plan verification in the visible verification browser and retry"}
	}
	if res.StatusCode != http.StatusOK {
		return &upstream.Error{Status: http.StatusBadGateway, Type: "start_plan_verification_failed", Message: "Start Plan browser verification failed; retry or check the verification service"}
	}
	var result struct {
		Param  string `json:"captcha_verify_param"`
		Region string `json:"captcha_region"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&result); err != nil || strings.TrimSpace(result.Param) == "" || strings.ContainsAny(result.Param, "\r\n\x00") || (result.Region != "cn" && result.Region != "sgp") {
		return &upstream.Error{Status: http.StatusBadGateway, Type: "start_plan_verification_failed", Message: "Start Plan verification service returned an invalid result"}
	}
	client.Headers["X-Aliyun-Captcha-Verify-Param"] = result.Param
	client.Headers["X-Aliyun-Captcha-Verify-Region"] = result.Region
	return nil
}

// 模型目录取套餐实际授权的 model capability，不把静态目录当成套餐权益。
func (s *Server) startPlanModels(ctx context.Context, cred credential.Credential) ([]config.ModelSpec, error) {
	if len(strings.Split(cred.APIKey, ".")) != 3 {
		s.logger.Printf("event=zcode_login stage=entitlement_check token_format=opaque")
	}
	if err := checkStartPlanToken(cred.APIKey); err != nil {
		s.logger.Printf("event=zcode_login stage=entitlement_check outcome=invalid_token")
		return nil, err
	}
	u, err := url.Parse(cred.BaseURL)
	if err != nil || !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/anthropic") {
		return nil, &upstream.Error{Status: http.StatusBadGateway, Type: "start_plan_configuration_error", Message: "Invalid Start Plan endpoint"}
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/anthropic") + "/billing/balance"
	query := u.Query()
	query.Set("app_version", s.cfg.Upstream.AppVersion)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.APIKey)
	req.Header.Set("Accept", "application/json")
	for name, value := range mimicHeaders(s.cfg.Upstream.AppVersion, s.cfg.Upstream.ClientTimezone, s.deviceID) {
		req.Header.Set(name, value)
	}
	res, err := s.loginHTTP.Do(req)
	if err != nil {
		s.logger.Printf("event=zcode_login stage=entitlement_check outcome=request_failed error_type=%T", err)
		return nil, &upstream.Error{Status: http.StatusBadGateway, Type: "start_plan_entitlement_unavailable", Message: "Unable to read Start Plan entitlements"}
	}
	defer res.Body.Close()
	s.logger.Printf("event=zcode_login stage=entitlement_check outcome=response http_status=%d", res.StatusCode)
	if res.StatusCode == http.StatusUnauthorized {
		return nil, &upstream.Error{Status: http.StatusUnauthorized, Type: "start_plan_reauthorization_required", Message: "Start Plan login was rejected; sign in again"}
	}
	var balance struct {
		Code *int   `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Balances []struct {
				Capabilities []string `json:"capabilities"`
				ExpiresAt    int64    `json:"expires_at"`
			} `json:"balances"`
		} `json:"data"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&balance)
	if res.StatusCode != http.StatusOK || decodeErr != nil || balance.Code == nil || *balance.Code != 0 {
		code := -1
		if balance.Code != nil {
			code = *balance.Code
		}
		message := strings.TrimSpace(balance.Msg)
		if len(message) > 160 || strings.ContainsAny(message, "\r\n") {
			message = "[redacted]"
		}
		message = regexp.MustCompile(`[A-Za-z0-9_-]{16,}`).ReplaceAllString(message, "[redacted]")
		s.logger.Printf("event=zcode_login stage=entitlement_check outcome=rejected http_status=%d response_code=%d decode_failed=%t message=%q", res.StatusCode, code, decodeErr != nil, message)
		return nil, &upstream.Error{Status: http.StatusBadGateway, Type: "start_plan_entitlement_unavailable", Message: "Start Plan entitlement response was rejected"}
	}
	allowed := make(map[string]bool)
	for _, bucket := range balance.Data.Balances {
		if bucket.ExpiresAt > 0 && bucket.ExpiresAt <= time.Now().Unix() {
			continue
		}
		for _, capability := range bucket.Capabilities {
			if strings.HasPrefix(capability, "model:") {
				allowed[strings.ToLower(strings.TrimPrefix(capability, "model:"))] = true
			}
		}
	}
	models := make([]config.ModelSpec, 0, len(s.cfg.Models))
	for _, model := range s.cfg.Models {
		if allowed[strings.ToLower(model.Upstream)] {
			models = append(models, model)
		}
	}
	return models, nil
}
