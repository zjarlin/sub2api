package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// 测试弹窗的「可测模型」契约：列表里的每个模型都应当能被该账号真实调用。
// 上游 /v1/models 往往不校验凭证（例如腾讯 TokenHub 对无效 key 也返回 200），
// 因此仅凭该端点的结果会让「能列出」与「能测通」脱钩。这里在列出候选模型之前
// 先做一次带缓存的账号级鉴权探测，把无凭证的账号挡在列表之外。
const (
	// 鉴权探测结论的缓存时长：同一账号在该窗口内只发一次真实请求，避免打开
	// 弹窗即刷上游。
	accountTestableAuthCacheTTL = 2 * time.Minute
	// 探测请求超时；失败按不可用处理，不影响主流程。
	accountTestableAuthProbeTimeout = 15 * time.Second
	// 探测只发一条最小请求，用于确认凭证/端点是否被接受。
	accountTestableAuthProbePrompt = "hi"
)

// AccountCredentialsInvalidError 表示账号凭证被上游拒绝，因而无法给出「可测」
// 模型列表。处理器据此直接返回错误，而不是回落到默认目录并把不可用模型展示为可选。
type AccountCredentialsInvalidError struct {
	Message string
}

func (e *AccountCredentialsInvalidError) Error() string {
	if e == nil {
		return "account credentials are invalid"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		return "account credentials are invalid"
	}
	return message
}

type accountTestableAuthVerdict struct {
	ok        bool
	message   string
	checkedAt time.Time
}

var accountTestableAuthCache sync.Map // map[int64]accountTestableAuthVerdict

// filterTestableOpenAIModels 收敛测试弹窗的候选模型：账号凭证无效时整体判定为
// 不可列出，凭证有效时剔除已有失败证据的模型。仅作用于测试/列表展示，不修改
// 共享上游目录，也不影响正式转发调度。
func (s *AccountTestService) filterTestableOpenAIModels(ctx context.Context, account *Account, models []openai.Model) ([]openai.Model, error) {
	if account == nil || len(models) == 0 {
		return models, nil
	}
	verdict := s.probeAccountAuthVerdict(ctx, account)
	if !verdict.ok {
		message := strings.TrimSpace(verdict.message)
		if message == "" {
			message = "该账号凭证无效，无法列出可测模型"
		}
		return nil, &AccountCredentialsInvalidError{Message: message}
	}
	// 有失败证据且无更新的成功证据的模型从测试列表移除；这是「能列出即可测」的
	// 第二重保证，避免目录里包含尚未被上游真正接受的 ID。
	filtered := make([]openai.Model, 0, len(models))
	for _, model := range models {
		if account.IsModelKnownUnsupported(model.ID) {
			continue
		}
		filtered = append(filtered, model)
	}
	return filtered, nil
}

// probeAccountAuthVerdict 返回账号凭证是否可用的结论，命中缓存时不发请求。
func (s *AccountTestService) probeAccountAuthVerdict(ctx context.Context, account *Account) accountTestableAuthVerdict {
	if account == nil {
		return accountTestableAuthVerdict{ok: false, message: "Account is required"}
	}
	now := time.Now()
	if cached, ok := accountTestableAuthCache.Load(account.ID); ok {
		if verdict, ok := cached.(accountTestableAuthVerdict); ok && now.Sub(verdict.checkedAt) < accountTestableAuthCacheTTL {
			return verdict
		}
	}
	verdict := s.runAccountAuthProbe(ctx, account)
	verdict.checkedAt = now
	if account.ID > 0 {
		accountTestableAuthCache.Store(account.ID, verdict)
	}
	return verdict
}

// runAccountAuthProbe 走账号真实的 chat/completions 端点做一次最小鉴权请求。
// 探测是「尽力而为」的守卫：只有从真实请求拿到明确的鉴权失败（401/403）才判定
// 不可用；缺少配置、网络错误、上游临时故障等一律保守放行，避免误伤列表。
func (s *AccountTestService) runAccountAuthProbe(ctx context.Context, account *Account) accountTestableAuthVerdict {
	if s == nil || s.cfg == nil || s.httpUpstream == nil {
		// 缺少探测依赖时无法验证，按放行处理。
		return accountTestableAuthVerdict{ok: true}
	}
	if !account.IsOpenAI() || account.Type != AccountTypeAPIKey {
		return accountTestableAuthVerdict{ok: true}
	}
	authToken := strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
	if authToken == "" {
		return accountTestableAuthVerdict{ok: false, message: "账号缺少 API Key，无法列出可测模型"}
	}
	baseURL := account.GetOpenAIBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		// base_url 校验失败不是凭证问题，保守放行。
		return accountTestableAuthVerdict{ok: true}
	}

	probeCtx, cancel := context.WithTimeout(ctx, accountTestableAuthProbeTimeout)
	defer cancel()

	// 探测必须与真实测试走同一条线路，否则结论与「能否测通」不一致：
	// Responses 账号用 /responses，其余 chat/completions 兼容账号用 /v1/chat/completions。
	probeModel := accountTestableAuthProbeModel(account)
	var apiURL string
	var payload map[string]any
	if openai_compat.ShouldUseResponsesAPI(account.Extra) {
		apiURL = buildOpenAIResponsesURLForPlatform(account.Platform, normalizedBaseURL)
		payload = createOpenAITestPayload(probeModel, false)
	} else {
		apiURL = buildOpenAIChatCompletionsURL(normalizedBaseURL)
		payload = createOpenAIChatCompletionsTestPayload(probeModel, accountTestableAuthProbePrompt)
	}
	payloadBytes, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return accountTestableAuthVerdict{ok: true}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken)
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	}
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	var tlsProfile *tlsfingerprint.Profile
	if s.tlsFPProfileService != nil {
		tlsProfile = s.tlsFPProfileService.ResolveTLSProfile(account)
	}
	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		// 网络层错误不代表凭证无效。
		return accountTestableAuthVerdict{ok: true}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return accountTestableAuthVerdict{ok: false, message: fmt.Sprintf("账号鉴权失败 (%d): %s", resp.StatusCode, truncateProbeMessage(message))}
	}
	return accountTestableAuthVerdict{ok: true}
}

// accountTestableAuthProbeModel 选一个确定性的具体模型用于鉴权探测；映射表是
// map，必须排序后再取，避免每次探测选中不同模型造成结论抖动。
func accountTestableAuthProbeModel(account *Account) string {
	if account != nil {
		candidates := make([]string, 0, len(account.GetModelMapping()))
		for model := range account.GetModelMapping() {
			model = strings.TrimSpace(model)
			if model != "" && !strings.Contains(model, "*") {
				candidates = append(candidates, model)
			}
		}
		if len(candidates) > 0 {
			sort.Strings(candidates)
			return candidates[0]
		}
	}
	return openai.DefaultTestModel
}

func truncateProbeMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 300 {
		return message[:300] + "..."
	}
	return message
}

// invalidateAccountTestableAuthCache 在账号凭证/状态变更后主动失效缓存，
// 使下一次列表请求重新探测。
func invalidateAccountTestableAuthCache(accountID int64) {
	if accountID > 0 {
		accountTestableAuthCache.Delete(accountID)
	}
}
