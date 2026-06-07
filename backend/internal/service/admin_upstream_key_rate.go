package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const (
	upstreamKeyRateDefaultLoginPath = "/api/v1/auth/login"
	upstreamKeyRateDefaultKeysPath  = "/api/v1/keys"
	upstreamKeyRateDefaultPageSize  = 100
	upstreamKeyRateDefaultMaxPages  = 5
	upstreamKeyRateMaxPageSize      = 500
	upstreamKeyRateMaxPages         = 20
	upstreamKeyRateRequestTimeout   = 20 * time.Second
	upstreamKeyRateMaxBodyBytes     = int64(2 * 1024 * 1024)
)

type ResolveUpstreamKeyRateInput struct {
	BaseURL   string `json:"base_url"`
	LoginPath string `json:"login_path"`
	KeysPath  string `json:"keys_path"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	APIKey    string `json:"api_key"`
	PageSize  int    `json:"page_size"`
	MaxPages  int    `json:"max_pages"`
}

type ResolveUpstreamKeyRateResult struct {
	RateMultiplier float64 `json:"rate_multiplier"`
	GroupID        *int64  `json:"group_id,omitempty"`
	GroupName      string  `json:"group_name,omitempty"`
	KeyID          *int64  `json:"key_id,omitempty"`
	KeyName        string  `json:"key_name,omitempty"`
	MatchedField   string  `json:"matched_field,omitempty"`
}

func (s *adminServiceImpl) ResolveUpstreamKeyRate(ctx context.Context, input ResolveUpstreamKeyRateInput) (*ResolveUpstreamKeyRateResult, error) {
	normalized, err := normalizeResolveUpstreamKeyRateInput(input)
	if err != nil {
		return nil, err
	}

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:            upstreamKeyRateRequestTimeout,
		ValidateResolvedIP: true,
	})
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_HTTP_CLIENT_FAILED", "failed to create upstream HTTP client").WithCause(err)
	}

	token, err := loginUpstreamForAccessToken(ctx, client, normalized)
	if err != nil {
		return nil, err
	}

	return findUpstreamKeyRate(ctx, client, normalized, token)
}

func normalizeResolveUpstreamKeyRateInput(input ResolveUpstreamKeyRateInput) (ResolveUpstreamKeyRateInput, error) {
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	input.LoginPath = strings.TrimSpace(input.LoginPath)
	input.KeysPath = strings.TrimSpace(input.KeysPath)
	input.Email = strings.TrimSpace(input.Email)
	input.Password = strings.TrimSpace(input.Password)
	input.APIKey = normalizeComparableAPIKey(input.APIKey)

	if input.BaseURL == "" {
		return input, infraerrors.BadRequest("UPSTREAM_RATE_BASE_URL_REQUIRED", "base_url is required")
	}
	base, err := parseUpstreamBaseURL(input.BaseURL)
	if err != nil {
		return input, err
	}
	input.BaseURL = base.String()

	if input.LoginPath == "" {
		input.LoginPath = upstreamKeyRateDefaultLoginPath
	}
	if input.KeysPath == "" {
		input.KeysPath = upstreamKeyRateDefaultKeysPath
	}
	if input.Email == "" {
		return input, infraerrors.BadRequest("UPSTREAM_RATE_EMAIL_REQUIRED", "email is required")
	}
	if input.Password == "" {
		return input, infraerrors.BadRequest("UPSTREAM_RATE_PASSWORD_REQUIRED", "password is required")
	}
	if input.APIKey == "" {
		return input, infraerrors.BadRequest("UPSTREAM_RATE_API_KEY_REQUIRED", "api_key is required")
	}
	if input.PageSize <= 0 {
		input.PageSize = upstreamKeyRateDefaultPageSize
	}
	if input.PageSize > upstreamKeyRateMaxPageSize {
		input.PageSize = upstreamKeyRateMaxPageSize
	}
	if input.MaxPages <= 0 {
		input.MaxPages = upstreamKeyRateDefaultMaxPages
	}
	if input.MaxPages > upstreamKeyRateMaxPages {
		input.MaxPages = upstreamKeyRateMaxPages
	}
	return input, nil
}

func parseUpstreamBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_BASE_URL_INVALID", "base_url is invalid").WithCause(err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_BASE_URL_INVALID", "base_url must use http or https")
	}
	if parsed.Host == "" {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_BASE_URL_INVALID", "base_url host is required")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/api/v1")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/v1")
	return parsed, nil
}

func loginUpstreamForAccessToken(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput) (string, error) {
	loginURL, err := buildUpstreamConsoleURL(input.BaseURL, input.LoginPath, 0, 0)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]string{
		"email":    input.Email,
		"password": input.Password,
	})
	if err != nil {
		return "", infraerrors.InternalServer("UPSTREAM_RATE_LOGIN_PAYLOAD_FAILED", "failed to encode upstream login payload").WithCause(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(body))
	if err != nil {
		return "", infraerrors.BadRequest("UPSTREAM_RATE_LOGIN_URL_INVALID", "login URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	payload, statusCode, err := doUpstreamJSON(client, req)
	if err != nil {
		return "", infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_LOGIN_REQUEST_FAILED", "upstream login request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return "", infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_LOGIN_FAILED", fmt.Sprintf("upstream login failed with HTTP %d", statusCode))
	}
	if err := assertUpstreamEnvelopeSuccess(payload, "UPSTREAM_RATE_LOGIN_FAILED", "upstream login failed"); err != nil {
		return "", err
	}

	token := extractUpstreamAccessToken(payload)
	if token == "" {
		return "", infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_TOKEN_MISSING", "upstream login response did not contain access_token")
	}
	return token, nil
}

func findUpstreamKeyRate(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput, token string) (*ResolveUpstreamKeyRateResult, error) {
	for page := 1; page <= input.MaxPages; page++ {
		keysURL, err := buildUpstreamConsoleURL(input.BaseURL, input.KeysPath, page, input.PageSize)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, keysURL, nil)
		if err != nil {
			return nil, infraerrors.BadRequest("UPSTREAM_RATE_KEYS_URL_INVALID", "keys URL is invalid").WithCause(err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		payload, statusCode, err := doUpstreamJSON(client, req)
		if err != nil {
			return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_KEYS_REQUEST_FAILED", "upstream keys request failed").WithCause(err)
		}
		if statusCode < 200 || statusCode >= 300 {
			return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_KEYS_FAILED", fmt.Sprintf("upstream keys request failed with HTTP %d", statusCode))
		}
		if err := assertUpstreamEnvelopeSuccess(payload, "UPSTREAM_RATE_KEYS_FAILED", "upstream keys request failed"); err != nil {
			return nil, err
		}

		items := extractUpstreamKeyItems(payload)
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			result, ok, err := matchUpstreamKeyRateItem(item, input.APIKey)
			if err != nil {
				return nil, err
			}
			if ok {
				return result, nil
			}
		}
	}

	return nil, infraerrors.NotFound(
		"UPSTREAM_RATE_KEY_NOT_FOUND",
		"upstream key was not found in the returned key list; the upstream may return masked keys or the page range may be too small",
	)
}

func buildUpstreamConsoleURL(baseURL, pathValue string, page, pageSize int) (string, error) {
	base, err := parseUpstreamBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	parsedPath, err := url.Parse(pathValue)
	if err != nil {
		return "", infraerrors.BadRequest("UPSTREAM_RATE_PATH_INVALID", "upstream path is invalid").WithCause(err)
	}
	if parsedPath.IsAbs() {
		if !strings.EqualFold(parsedPath.Scheme, base.Scheme) || !strings.EqualFold(parsedPath.Host, base.Host) {
			return "", infraerrors.BadRequest("UPSTREAM_RATE_PATH_INVALID", "upstream path must stay under base_url host")
		}
		base.Path = strings.TrimRight(parsedPath.Path, "/")
		base.RawQuery = parsedPath.RawQuery
	} else {
		pathPart := strings.TrimSpace(parsedPath.Path)
		if pathPart == "" {
			pathPart = "/"
		}
		if !strings.HasPrefix(pathPart, "/") {
			pathPart = "/" + pathPart
		}
		base.Path = strings.TrimRight(base.Path, "/") + pathPart
		base.RawQuery = parsedPath.RawQuery
	}
	if page > 0 {
		q := base.Query()
		if q.Get("page") == "" {
			q.Set("page", strconv.Itoa(page))
		}
		if q.Get("page_size") == "" {
			q.Set("page_size", strconv.Itoa(pageSize))
		}
		if q.Get("sort_by") == "" {
			q.Set("sort_by", "created_at")
		}
		if q.Get("sort_order") == "" {
			q.Set("sort_order", "desc")
		}
		base.RawQuery = q.Encode()
	}
	return base.String(), nil
}

func doUpstreamJSON(client *http.Client, req *http.Request) (map[string]any, int, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, upstreamKeyRateMaxBodyBytes)
	var payload map[string]any
	if err := json.NewDecoder(limited).Decode(&payload); err != nil {
		return nil, resp.StatusCode, err
	}
	return payload, resp.StatusCode, nil
}

func assertUpstreamEnvelopeSuccess(payload map[string]any, reason, fallbackMessage string) error {
	code, ok := numericField(payload, "code")
	if !ok || code == 0 {
		return nil
	}
	message := stringField(payload, "message", "msg", "error")
	if message == "" {
		message = fallbackMessage
	}
	return infraerrors.New(http.StatusBadGateway, reason, message)
}

func extractUpstreamAccessToken(payload map[string]any) string {
	if token := stringField(payload, "access_token", "accessToken", "token"); token != "" {
		return token
	}
	data, _ := mapField(payload, "data")
	if data == nil {
		return ""
	}
	return stringField(data, "access_token", "accessToken", "token")
}

func extractUpstreamKeyItems(payload map[string]any) []map[string]any {
	if items := arrayMapsField(payload, "items", "list", "keys", "tokens"); len(items) > 0 {
		return items
	}
	if dataItems := arrayMapsField(payload, "data"); len(dataItems) > 0 {
		return dataItems
	}
	data, _ := mapField(payload, "data")
	if data == nil {
		return nil
	}
	if items := arrayMapsField(data, "items", "list", "keys", "tokens", "records"); len(items) > 0 {
		return items
	}
	if nestedDataItems := arrayMapsField(data, "data"); len(nestedDataItems) > 0 {
		return nestedDataItems
	}
	nestedData, _ := mapField(data, "data")
	if nestedData == nil {
		return nil
	}
	return arrayMapsField(nestedData, "items", "list", "keys", "tokens", "records")
}

func matchUpstreamKeyRateItem(item map[string]any, targetAPIKey string) (*ResolveUpstreamKeyRateResult, bool, error) {
	for _, field := range []string{"key", "api_key", "apiKey", "token", "value", "sk", "key_value", "keyValue"} {
		candidate := normalizeComparableAPIKey(stringField(item, field))
		if candidate == "" || candidate != targetAPIKey {
			continue
		}
		result, err := extractUpstreamKeyRateResult(item)
		if err != nil {
			return nil, false, err
		}
		result.MatchedField = field
		return result, true, nil
	}
	return nil, false, nil
}

func extractUpstreamKeyRateResult(item map[string]any) (*ResolveUpstreamKeyRateResult, error) {
	group, _ := mapField(item, "group")
	rate, ok := numericField(item, "group_rate_multiplier", "groupRateMultiplier", "rate_multiplier", "rateMultiplier")
	if !ok && group != nil {
		rate, ok = numericField(group, "rate_multiplier", "rateMultiplier", "group_rate_multiplier", "groupRateMultiplier", "ratio", "group_ratio", "groupRatio")
	}
	if !ok {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_MULTIPLIER_MISSING", "matched upstream key does not contain group rate_multiplier")
	}

	result := &ResolveUpstreamKeyRateResult{
		RateMultiplier: rate,
		KeyName:        stringField(item, "name", "key_name", "keyName"),
	}
	if keyID, ok := int64Field(item, "id", "key_id", "keyId", "token_id", "tokenId"); ok {
		result.KeyID = &keyID
	}
	if group != nil {
		result.GroupName = stringField(group, "name", "group_name", "groupName")
		if groupID, ok := int64Field(group, "id", "group_id", "groupId"); ok {
			result.GroupID = &groupID
		}
	}
	if result.GroupName == "" {
		result.GroupName = stringField(item, "group_name", "groupName")
	}
	if result.GroupID == nil {
		if groupID, ok := int64Field(item, "group_id", "groupId"); ok {
			result.GroupID = &groupID
		}
	}
	return result, nil
}

func normalizeComparableAPIKey(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "Bearer ")
	value = strings.TrimPrefix(value, "bearer ")
	return strings.TrimSpace(value)
}

func stringField(m map[string]any, names ...string) string {
	for _, name := range names {
		raw, ok := m[name]
		if !ok || raw == nil {
			continue
		}
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func mapField(m map[string]any, name string) (map[string]any, bool) {
	raw, ok := m[name]
	if !ok || raw == nil {
		return nil, false
	}
	if typed, ok := raw.(map[string]any); ok {
		return typed, true
	}
	return nil, false
}

func arrayMapsField(m map[string]any, names ...string) []map[string]any {
	for _, name := range names {
		raw, ok := m[name]
		if !ok || raw == nil {
			continue
		}
		values, ok := raw.([]any)
		if !ok {
			continue
		}
		items := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if item, ok := value.(map[string]any); ok {
				items = append(items, item)
			}
		}
		return items
	}
	return nil
}

func numericField(m map[string]any, names ...string) (float64, bool) {
	for _, name := range names {
		raw, ok := m[name]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case float64:
			return v, true
		case float32:
			return float64(v), true
		case int:
			return float64(v), true
		case int64:
			return float64(v), true
		case json.Number:
			n, err := v.Float64()
			return n, err == nil
		case string:
			n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func int64Field(m map[string]any, names ...string) (int64, bool) {
	for _, name := range names {
		raw, ok := m[name]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case float64:
			return int64(v), true
		case int:
			return int64(v), true
		case int64:
			return v, true
		case json.Number:
			n, err := v.Int64()
			return n, err == nil
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err == nil {
				return n, true
			}
		}
	}
	return 0, false
}
