package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
)

// VisionImageProbeWarningCode 是目录同步响应里读图探测结论的告警码。
const VisionImageProbeWarningCode = "vision_image_input_probe"

// VisionImageProbeSource 标记由真实读图探测写入的能力快照。它属于最有力的证据：
// 网关确实把一张带独特颜色的图片发给该上游模型，并从回答里读回了这个颜色。
const VisionImageProbeSource = "vision-image-probe"

// 探测使用一张 1x1 纯色 PNG，并要求模型只回答一个词。判定只看回答里是否出现
// 该颜色，不接受“我无法查看图片”这类套话，避免把纯文本模型误判成支持视觉。
const (
	visionImageProbeTimeout      = 20 * time.Second
	visionImageProbeMaxOutput    = 64
	visionImageProbeMaxBodyBytes = 64 << 10
	visionImageProbePrompt       = "What is the dominant color of this image? Answer with exactly one English color word."
)

// 16x16 纯红（#FF0000）、完全不透明的 PNG。
//
// 早期版本用 1x1、alpha=127 的半透明红：叠到白底后实际是粉色，上游如实回答
// “pink/salmon”却被判成“没看图”，把支持视觉的模型误标为不支持（2026-10-21 实测
// 复现于 tokenav.ai）。判定证据必须出自不依赖渲染假设的纯色，因此改用不透明纯红。
const visionImageProbePNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAFklEQVR42mP4z8BAEmIY1TCqYfhqAACQ+f8B8u7oVwAAAABJRU5ErkJggg=="

// 纯红在不同模型口中的常见叫法都要接受，否则“读到了图”会被误判成“没读到”。
var visionImageProbeColorWords = []string{
	"red", "crimson", "scarlet", "vermilion", "cherry",
	"reddish", "rose", "ruby", "红",
}

// VisionImageProbeResult 是单次探测的结论；Inconclusive 表示本次证据不足以判定，
// 调用方不得据此改写账号能力（网络失败、限流、鉴权问题都不构成能力证据）。
type VisionImageProbeResult struct {
	SupportsImage bool
	Conclusive    bool
	StatusCode    int
	Message       string
}

// ProbeOpenAIModelImageInput 用真实图片请求判定该账号的指定上游模型能否直接读图。
//
// 只走 Chat Completions：第三方 OpenAI 兼容上游普遍支持该端点，而 /v1/responses
// 的实现差异很大（同一上游的不同中转经常只实现其中一个）。读图能力属于模型能力，
// 与协议无关，因此用兼容性最好的端点探测。
func (s *AccountTestService) ProbeOpenAIModelImageInput(ctx context.Context, account *Account, upstreamModel string) VisionImageProbeResult {
	if s == nil || account == nil || s.httpUpstream == nil {
		return VisionImageProbeResult{}
	}
	upstreamModel = strings.TrimSpace(upstreamModel)
	apiKey := account.GetOpenAIApiKey()
	if upstreamModel == "" || apiKey == "" {
		return VisionImageProbeResult{}
	}
	baseURL := account.GetOpenAIBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		logger.LegacyPrintf("service.vision_probe", "probe_invalid_baseurl: account_id=%d err=%v", account.ID, err)
		return VisionImageProbeResult{}
	}

	body, err := json.Marshal(map[string]any{
		"model":      upstreamModel,
		"stream":     false,
		"max_tokens": visionImageProbeMaxOutput,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": visionImageProbePrompt},
				{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + visionImageProbePNGBase64}},
			},
		}},
	})
	if err != nil {
		return VisionImageProbeResult{}
	}

	probeCtx, cancel := context.WithTimeout(ctx, visionImageProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, buildOpenAIChatCompletionsURL(normalizedBaseURL), bytes.NewReader(body))
	if err != nil {
		return VisionImageProbeResult{}
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
	if err != nil {
		logger.LegacyPrintf("service.vision_probe", "probe_request_failed: account_id=%d model=%s err=%v", account.ID, upstreamModel, err)
		return VisionImageProbeResult{}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, visionImageProbeMaxBodyBytes))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, visionImageProbeMaxBodyBytes))
	if readErr != nil {
		return VisionImageProbeResult{}
	}
	return judgeVisionImageProbe(upstreamModel, resp.StatusCode, raw)
}

// judgeVisionImageProbe 把探测响应折算成能力结论。
//
//   - 回答里出现该颜色 → 确实读到了图，支持视觉。
//   - 2xx 却答不出颜色（含“无法查看图片”）→ 不支持视觉。
//   - 401/403/429/5xx、模型不存在、超时 → 无能力证据，保持 unknown。
func judgeVisionImageProbe(model string, status int, body []byte) VisionImageProbeResult {
	result := VisionImageProbeResult{StatusCode: status}
	if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		result.Message = truncateString(extractUpstreamErrorMessage(body), 200)
		return result
	}
	if isResponsesProbeModelUnavailable(status, body) || isOpenAICompatibleModelNotFoundBody(body) {
		result.Message = truncateString(extractUpstreamErrorMessage(body), 200)
		return result
	}
	answer := strings.ToLower(chatCompletionProbeText(body))
	if status < 200 || status >= 300 {
		result.Message = truncateString(firstNonEmptyString(answer, extractUpstreamErrorMessage(body)), 200)
		// 明确的 4xx 能力拒绝是有效证据：上游认得出请求但拒绝图片。
		if status >= http.StatusBadRequest && status < http.StatusInternalServerError && isImageInputRejection(answer, extractUpstreamErrorMessage(body)) {
			result.Conclusive = true
			return result
		}
		return result
	}
	result.Conclusive = true
	result.SupportsImage = visionProbeAnswerNamesColor(answer)
	result.Message = truncateString(answer, 200)
	if !result.SupportsImage {
		logger.LegacyPrintf("service.vision_probe", "probe_no_color: model=%s status=%d answer=%q", model, status, result.Message)
	}
	return result
}

// 只有回答里出现颜色词才算读到图；空回答或“无法查看图片”都不算。
func visionProbeAnswerNamesColor(answer string) bool {
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer == "" {
		return false
	}
	for _, word := range visionImageProbeColorWords {
		if strings.Contains(answer, word) {
			return true
		}
	}
	return false
}

// 上游明确表示“不支持图像输入”时才是负面证据；泛化的 400 可能只是参数问题。
func isImageInputRejection(answer, upstreamMessage string) bool {
	combined := strings.ToLower(strings.TrimSpace(answer + " " + upstreamMessage))
	for _, phrase := range []string{
		"does not support image", "image input is not supported", "text-only", "text only",
		"不支持图片", "不支持图像", "仅支持文本", "只支持文本",
	} {
		if strings.Contains(combined, phrase) {
			return true
		}
	}
	return false
}

// 兼容 Chat Completions 的多种上游回包：content 可能是字符串、也可能是分块数组；
// 推理型模型还可能把结论放在 reasoning_content 里（线上实测 deepseek-v4.1-flash 就是这样）。
func chatCompletionProbeText(body []byte) string {
	var parts []string
	for _, item := range gjson.GetBytes(body, "choices").Array() {
		message := item.Get("message")
		if content := message.Get("content"); content.Exists() {
			if content.Type == gjson.String {
				parts = append(parts, content.String())
			} else {
				for _, chunk := range content.Array() {
					parts = append(parts, chunk.Get("text").String())
				}
			}
		}
		if reasoning := message.Get("reasoning_content").String(); reasoning != "" {
			parts = append(parts, reasoning)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, " "))
	return text
}

// ApplyVisionImageProbeResult 把确凿的探测结论写回账号能力快照。
// 只写 Conclusive 的结论；未定论时保持原样，避免把限流/故障固化成能力缺失。
func (s *AccountTestService) ApplyVisionImageProbeResult(ctx context.Context, account *Account, upstreamModel string, result VisionImageProbeResult) error {
	if s == nil || s.accountRepo == nil || account == nil || !result.Conclusive {
		return nil
	}
	upstreamModel = strings.TrimSpace(upstreamModel)
	if upstreamModel == "" {
		return nil
	}
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	if snapshot == nil {
		snapshot = &UpstreamModelMetadataSnapshot{Source: VisionImageProbeSource, Models: map[string]UpstreamModelMetadata{}}
	}
	models := make(map[string]UpstreamModelMetadata, len(snapshot.Models)+1)
	for id, metadata := range snapshot.Models {
		models[id] = metadata
	}
	entry := models[upstreamModel]
	entry.ID = firstNonEmptyString(strings.TrimSpace(entry.ID), upstreamModel)
	modalities := []string{"text"}
	if result.SupportsImage {
		modalities = append(modalities, "image")
	}
	entry.InputModalities = modalities
	models[upstreamModel] = entry
	updated := &UpstreamModelMetadataSnapshot{
		Source:   VisionImageProbeSource,
		SyncedAt: time.Now().UTC().Format(time.RFC3339),
		Models:   models,
	}
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{UpstreamModelMetadataExtraKey: updated}); err != nil {
		return err
	}
	account.SetUpstreamModelMetadataSnapshot(*updated)
	return nil
}

// FirstConfiguredUpstreamModel 返回账号 model_mapping 里第一个可探测的具体上游模型。
// 同步目录后用它做读图探测：这是管理员实际配置过的目标，比目录里任意一条更能代表该账号。
func FirstConfiguredUpstreamModel(account *Account) string {
	if account == nil {
		return ""
	}
	mapping := account.GetModelMapping()
	models := make([]string, 0, len(mapping))
	for _, upstream := range mapping {
		upstream = strings.TrimSpace(upstream)
		if upstream == "" || strings.Contains(upstream, "*") || isCodexDedicatedMediaModel(upstream) {
			continue
		}
		models = append(models, upstream)
	}
	if len(models) == 0 {
		return ""
	}
	sort.Strings(models)
	return models[0]
}
