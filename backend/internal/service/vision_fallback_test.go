package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func visionTestConfig() *config.Config {
	return &config.Config{
		Gateway:  config.GatewayConfig{VisionFallback: config.GatewayVisionFallbackConfig{Enabled: true}},
		Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{AllowInsecureHTTP: true}},
	}
}

func visionTestAccount(id int64, model string, modalities ...string) Account {
	account := Account{
		ID: id, Name: model, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "test-key", "base_url": "http://upstream.example",
			"model_mapping": map[string]any{model: model},
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)},
	}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Models: map[string]UpstreamModelMetadata{model: {ID: model, InputModalities: modalities}},
	})
	return account
}

func visionTestContext(body []byte, keyID, groupID int64) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &APIKey{ID: keyID, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformOpenAI}})
	return c, recorder
}

func visionTestResponse(model, text string) *http.Response {
	body, _ := json.Marshal(map[string]any{
		"id": "completion-" + model, "object": "chat.completion", "model": model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18},
	})
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
}

const visionTestInput = `{"model":"text-model","input":[{"role":"user","content":[{"type":"input_text","text":"Explain the error"},{"type":"input_image","image_url":"data:image/png;base64,AAAA","detail":"original"}]}],"stream":false}`

func TestVisionFallbackForwardKeepsPrimaryModelAndSeparatesUsage(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	helper := visionTestAccount(2, "vision-model", "text", "image")
	var calls []string
	svc := &OpenAIGatewayService{
		cfg:         visionTestConfig(),
		accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {primary, helper}}},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			model := gjson.GetBytes(body, "model").String()
			calls = append(calls, model)
			require.Equal(t, "/v1/chat/completions", req.URL.Path)
			if model == "vision-model" {
				require.Equal(t, int64(2), accountID)
				require.Contains(t, string(body), "data:image/png;base64,AAAA")
				require.Contains(t, string(body), visionDescriptionPrompt)
				require.NotContains(t, string(body), `"original"`)
				require.False(t, gjson.GetBytes(body, "stream").Bool())
				_, hasDeadline := req.Context().Deadline()
				require.True(t, hasDeadline)
				require.NoError(t, req.Context().Err())
				return visionTestResponse(model, "红色提示：此模型不支持图像输入。"), nil
			}
			require.Equal(t, int64(1), accountID)
			require.NotContains(t, string(body), "data:image")
			require.Contains(t, string(body), "此模型不支持图像输入")
			require.Contains(t, string(body), "Explain the error")
			return visionTestResponse(model, "主模型解释"), nil
		}},
	}
	body := []byte(visionTestInput)
	c, recorder := visionTestContext(body, 9, 7)
	result, err := svc.Forward(context.Background(), c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, []string{"vision-model", "text-model"}, calls)
	require.Equal(t, "text-model", result.Model)
	require.Equal(t, "主模型解释", gjson.Get(recorder.Body.String(), "output.0.content.0.text").String())
	require.NotContains(t, recorder.Body.String(), "vision-model")
	usage := TakeVisionFallbackUsage(c)
	require.Len(t, usage, 1)
	require.Equal(t, int64(2), usage[0].Account.ID)
	require.Equal(t, 11, usage[0].Result.Usage.InputTokens)
	require.Equal(t, "/v1/chat/completions", usage[0].Result.UpstreamEndpoint)
	require.NotEmpty(t, usage[0].PayloadHash)
	for _, key := range []ctxkey.Key{ctxkey.RequestID, ctxkey.ClientRequestID} {
		billingCtx := context.WithValue(context.Background(), key, "shared-parent-request")
		helperID := resolveUsageBillingRequestID(billingCtx, usage[0].Result.RequestID)
		require.NotEqual(t, resolveUsageBillingRequestID(billingCtx, result.RequestID), helperID)
		require.Equal(t, helperID, resolveUsageBillingRequestID(billingCtx, usage[0].Result.RequestID))
	}
	require.Empty(t, TakeVisionFallbackUsage(c))
	// 同一个用户重放图片时只请求主模型；不同 API Key 不能共享摘要。
	c, _ = visionTestContext(body, 9, 7)
	_, err = svc.Forward(context.Background(), c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, []string{"vision-model", "text-model", "text-model"}, calls)
	require.Empty(t, TakeVisionFallbackUsage(c))
	c, _ = visionTestContext(body, 10, 7)
	_, err = svc.Forward(context.Background(), c, &primary, body)
	require.NoError(t, err)
	require.Len(t, calls, 5)
	secondUsage := TakeVisionFallbackUsage(c)
	require.Len(t, secondUsage, 1)
	require.NotEqual(t, usage[0].Result.RequestID, secondUsage[0].Result.RequestID)
}

func TestVisionFallbackPreservesToolsAndOnlyTransformsMediaParts(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	helper := visionTestAccount(2, "vision-model", "text", "image")
	calls := 0
	svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {helper}}},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			calls++
			return visionTestResponse("vision-model", fmt.Sprintf("图片 %d 的描述", calls)), nil
		}},
	}
	body := []byte(`{"model":"text-model","stream":true,"previous_response_id":"resp_previous","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"input":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"type\":\"input_image\"}"},{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"工具结果"},{"type":"input_image","image_url":"https://example.com/first.png"}]},{"role":"user","content":[{"type":"input_text","text":"比较两张图"},{"type":"input_image","image_url":"https://example.com/second.png"}]}]}`)
	c, _ := visionTestContext(body, 9, 7)
	got, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	for _, path := range []string{"model", "stream", "previous_response_id", "tools", "input.0", "input.1.call_id", "input.1.output.0", "input.2.role"} {
		var original, updated any
		require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(body, path).Raw), &original))
		require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(got, path).Raw), &updated))
		require.Equal(t, original, updated, path)
	}
	require.Equal(t, "input_text", gjson.GetBytes(got, "input.1.output.1.type").String())
	require.Contains(t, gjson.GetBytes(got, "input.1.output.1.text").String(), "图片 1")
	require.Contains(t, gjson.GetBytes(got, "input.2.content.1.text").String(), "图片 2")
	require.NotContains(t, string(got), "https://example.com")
}

func TestVisionFallbackDoesNotCallHelperForNativeOrTextRequests(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: visionTestConfig()}
	for _, tc := range []struct {
		name, body      string
		native, enabled bool
	}{
		{"native", visionTestInput, true, true},
		{"plain", `{"model":"text-model","input":"Hi"}`, false, true},
		{"text_mentions_image", `{"model":"text-model","input":[{"role":"user","content":[{"type":"input_text","text":"input_image"}]}]}`, false, true},
		{"disabled", visionTestInput, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := visionTestAccount(1, "text-model", "text")
			if tc.native {
				primary = visionTestAccount(1, "text-model", "text", "image")
			}
			svc.cfg.Gateway.VisionFallback.Enabled = tc.enabled
			body := []byte(tc.body)
			c, _ := visionTestContext(body, 9, 7)
			got, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
			require.NoError(t, err)
			require.Equal(t, body, got)
		})
	}
}

func TestVisionFallbackFailureDoesNotCallPrimaryOrCacheEmptyDescription(t *testing.T) {
	for _, helperText := range []string{"", strings.Repeat("a", visionDescriptionMaxBytes+1)} {
		primary := visionTestAccount(1, "text-model", "text")
		helper := visionTestAccount(2, "vision-model", "text", "image")
		calls := 0
		svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {helper}}},
			httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
				calls++
				require.Equal(t, int64(2), accountID)
				return visionTestResponse("vision-model", helperText), nil
			}},
		}
		c, recorder := visionTestContext([]byte(visionTestInput), 9, 7)
		_, err := svc.Forward(context.Background(), c, &primary, []byte(visionTestInput))
		require.Error(t, err)
		require.Equal(t, 1, calls)
		require.Equal(t, http.StatusBadGateway, recorder.Code)
		require.Empty(t, svc.visionFallbackCache.entries)
		require.Len(t, TakeVisionFallbackUsage(c), 1)
	}
}

func TestVisionFallbackFailureIsReplayableWithoutBlamingPrimaryAccount(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	helper := visionTestAccount(2, "vision-model", "text", "image")
	svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {helper}}},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
			require.Equal(t, helper.ID, accountID)
			return &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"private-provider-detail"}}`))}, nil
		}},
	}
	c, recorder := visionTestContext([]byte(visionTestInput), 9, 7)
	_, err := svc.Forward(context.Background(), c, &primary, []byte(visionTestInput))
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.False(t, failoverErr.ShouldReportAccountScheduleFailure())
	require.Equal(t, http.StatusBadGateway, failoverErr.ClientStatusCode)
	require.Equal(t, "The vision helper could not describe the image; please retry later", failoverErr.ClientMessage)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "private-provider-detail")
	value, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := value.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, helper.ID, events[0].AccountID)
	require.Equal(t, "The vision helper could not describe the image; please retry later", events[0].Message)
}

func TestVisionFallbackInputValidationBeforeAnyHelperCall(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	svc := &OpenAIGatewayService{cfg: visionTestConfig()}
	for _, content := range []string{
		`[{"type":"input_image","file_id":"file_from_other_provider"}]`,
		`[{"type":"input_image","image_url":"file:///tmp/private.png"}]`,
		`[{"type":"input_image","image_url":"http://example.com/image.png"}]`,
		`[` + strings.Repeat(`{"type":"input_image","image_url":"data:image/png;base64,AAAA"},`, 9) + `{"type":"input_image","file_id":"file_from_other_provider"}]`,
	} {
		body := []byte(`{"model":"text-model","input":[{"role":"user","content":` + content + `}]}`)
		c, _ := visionTestContext(body, 9, 7)
		_, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
		var failure *visionFallbackError
		require.ErrorAs(t, err, &failure)
		require.Equal(t, http.StatusBadRequest, failure.status)
	}
}

func TestVisionFallbackGroupIsolationAndDisabledHelper(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	helper := visionTestAccount(2, "vision-model", "text", "image")
	svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{8: {helper}}}}
	c, _ := visionTestContext([]byte(visionTestInput), 9, 7)
	_, err := svc.prepareVisionFallback(context.Background(), c, &primary, []byte(visionTestInput))
	require.ErrorContains(t, err, "No native vision helper")
	helper.Schedulable = false
	require.Empty(t, visionFallbackCandidates([]Account{helper}, svc.cfg, nil))
	helper.Schedulable = true
	svc.cfg.Gateway.VisionFallback.Model = "not-configured"
	require.Len(t, visionFallbackCandidates([]Account{helper}, svc.cfg, nil), 1)
	svc.cfg.Gateway.VisionFallback.Model = "vision-model"
	require.Len(t, visionFallbackCandidates([]Account{helper}, svc.cfg, nil), 1)
}

func TestVisionFallbackManifestETagChangesWithHelperAvailability(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	helper := visionTestAccount(2, "vision-model", "text", "image")
	repo := &countingCodexModelsAccountRepo{accounts: []Account{primary, helper}}
	svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: repo}
	group := &Group{ID: 7, Platform: PlatformOpenAI}
	manifest, configured, err := svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), group, "")
	require.NoError(t, err)
	require.True(t, configured)
	var modalities []string
	for _, model := range gjson.GetBytes(manifest.Body, "models").Array() {
		if model.Get("slug").String() == "text-model" {
			require.NoError(t, json.Unmarshal([]byte(model.Get("input_modalities").Raw), &modalities))
			require.False(t, model.Get("supports_image_detail_original").Bool())
		}
	}
	require.Equal(t, []string{"text", "image"}, modalities)
	cached, _, err := svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), group, manifest.ETag)
	require.NoError(t, err)
	require.True(t, cached.NotModified)
	repo.accounts[1].Schedulable = false
	changed, _, err := svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), group, manifest.ETag)
	require.NoError(t, err)
	require.False(t, changed.NotModified)
	require.NotEqual(t, manifest.ETag, changed.ETag)
	for _, model := range gjson.GetBytes(changed.Body, "models").Array() {
		if model.Get("slug").String() == "text-model" {
			require.JSONEq(t, `["text"]`, model.Get("input_modalities").Raw)
		}
	}
}

func TestVisionFallbackManifestUsesVerifiedNativeVisionDespiteInactiveCatalogAccount(t *testing.T) {
	primary := visionTestAccount(831, "gpt-6-astra", "text", "image")
	primary.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Source: "verified-responses-image",
		Models: map[string]UpstreamModelMetadata{
			"gpt-6-astra": {ID: "gpt-6-astra", InputModalities: []string{"text", "image"}},
		},
	})
	inactive := visionTestAccount(820, "gpt-6-astra")
	inactive.Schedulable = false
	cfg := visionTestConfig()
	cfg.Gateway.VisionFallback.Enabled = false
	repo := splitCodexModelsAccountRepo{
		schedulable: map[int64][]Account{7: {primary}},
		catalog:     map[int64][]Account{7: {primary, inactive}},
	}
	svc := &OpenAIGatewayService{cfg: cfg, accountRepo: repo}
	manifest, configured, err := svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), &Group{ID: 7, Platform: PlatformOpenAI}, "")
	require.NoError(t, err)
	require.True(t, configured)
	model := gjson.GetBytes(manifest.Body, "models.0")
	require.Equal(t, "gpt-6-astra", model.Get("slug").String())
	require.JSONEq(t, `["text","image"]`, model.Get("input_modalities").Raw)

	unknown := visionTestAccount(821, "gpt-6-astra")
	previousETag := manifest.ETag
	repo.schedulable[7] = []Account{primary, unknown}
	repo.catalog[7] = []Account{primary, inactive, unknown}
	manifest, configured, err = svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), &Group{ID: 7, Platform: PlatformOpenAI}, "")
	require.NoError(t, err)
	require.True(t, configured)
	require.JSONEq(t, `["text","image"]`, gjson.GetBytes(manifest.Body, "models.0.input_modalities").Raw)
	require.Equal(t, previousETag, manifest.ETag)

	// 已验证账号临时不可调度时，新账号仍使用同一 GPT 型号的能力声明。
	primary.Schedulable = false
	repo.schedulable[7] = []Account{unknown}
	repo.catalog[7] = []Account{primary, inactive, unknown}
	manifest, configured, err = svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), &Group{ID: 7, Platform: PlatformOpenAI}, "")
	require.NoError(t, err)
	require.True(t, configured)
	require.JSONEq(t, `["text","image"]`, gjson.GetBytes(manifest.Body, "models.0.input_modalities").Raw)

	// 明确的负面能力证据不能被其他账号的成功记录覆盖。
	unknown = visionTestAccount(821, "gpt-6-astra", "text")
	repo.schedulable[7] = []Account{unknown}
	repo.catalog[7] = []Account{primary, inactive, unknown}
	manifest, configured, err = svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), &Group{ID: 7, Platform: PlatformOpenAI}, "")
	require.NoError(t, err)
	require.True(t, configured)
	require.JSONEq(t, `["text"]`, gjson.GetBytes(manifest.Body, "models.0.input_modalities").Raw)
}

func TestVisionFallbackVerifiedCapabilityDoesNotCrossModelMappings(t *testing.T) {
	verified := visionTestAccount(1, "gpt-6-astra", "text", "image")
	snapshot := verified.GetUpstreamModelMetadataSnapshot()
	snapshot.Source = "verified-responses-image"
	verified.SetUpstreamModelMetadataSnapshot(*snapshot)
	unknown := visionTestAccount(2, "gpt-6-sol")
	verified.Credentials["model_mapping"] = map[string]any{"coder": "gpt-6-astra"}
	unknown.Credentials["model_mapping"] = map[string]any{"coder": "gpt-6-sol"}
	require.False(t, groupModelHasNativeVision([]Account{verified, unknown}, PlatformOpenAI, "coder"))
	require.False(t, groupModelHasNativeVision([]Account{unknown}, PlatformOpenAI, "coder"))
}

func TestVisionFallbackCacheExpiryAndCancellation(t *testing.T) {
	var cache visionDescriptionCache
	key := [32]byte{1}
	cache.put(key, "description")
	value, ok := cache.get(key)
	require.True(t, ok)
	require.Equal(t, "description", value)
	cache.entries[key] = visionDescriptionEntry{text: value, expires: time.Now().Add(-time.Second)}
	_, ok = cache.get(key)
	require.False(t, ok)
	ctx, cancel := context.WithCancel(context.Background())
	upstream, release := detachUpstreamContext(context.WithValue(ctx, visionFallbackContextKey{}, true))
	defer release()
	cancel()
	require.ErrorIs(t, upstream.Err(), context.Canceled)
}

func TestVisionFallbackStreamsOnlyPrimaryResponse(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	helper := visionTestAccount(2, "vision-model", "text", "image")
	var calls []string
	svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {helper}}},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			calls = append(calls, gjson.GetBytes(body, "model").String())
			if accountID == 2 {
				return visionTestResponse("vision-model", "private-helper-description"), nil
			}
			require.True(t, gjson.GetBytes(body, "stream").Bool())
			require.Contains(t, string(body), "private-helper-description")
			events := "data: {\"id\":\"chat-main\",\"model\":\"text-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"final answer\"},\"finish_reason\":null}]}\n\n" +
				"data: {\"id\":\"chat-main\",\"model\":\"text-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\n" + "data: [DONE]\n\n"
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(events))}, nil
		}},
	}
	body := []byte(strings.Replace(visionTestInput, `"stream":false`, `"stream":true`, 1))
	c, recorder := visionTestContext(body, 9, 7)
	result, err := svc.Forward(context.Background(), c, &primary, body)
	require.NoError(t, err)
	require.True(t, result.Stream)
	require.Equal(t, []string{"vision-model", "text-model"}, calls)
	require.Contains(t, recorder.Body.String(), "response.completed")
	require.Contains(t, recorder.Body.String(), "final answer")
	require.NotContains(t, recorder.Body.String(), "private-helper-description")
	require.NotContains(t, recorder.Body.String(), "vision-model")
}

func TestVisionFallbackNativeResponsesHelperRequiresCompletedStatus(t *testing.T) {
	for _, status := range []string{"completed", "incomplete", "failed"} {
		t.Run(status, func(t *testing.T) {
			primary := visionTestAccount(1, "text-model", "text")
			helper := visionTestAccount(2, "vision-model", "text", "image")
			helper.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceResponses)
			svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {helper}}},
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
					require.Equal(t, int64(2), accountID)
					require.Equal(t, "/v1/responses", req.URL.Path)
					require.NoError(t, req.Context().Err())
					requestBody, readErr := io.ReadAll(req.Body)
					require.NoError(t, readErr)
					require.False(t, gjson.GetBytes(requestBody, "tools").Exists(), "辅助回合不注入生图工具")
					body := fmt.Sprintf(`{"id":"resp-helper","object":"response","model":"vision-model","status":%q,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"visible details"}]}],"usage":{"input_tokens":11,"output_tokens":7}}`, status)
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				}},
			}
			svc.cfg.Gateway.CodexImageGenerationBridgeEnabled = true
			svc.cfg.Gateway.ForceCodexCLI = true
			c, _ := visionTestContext([]byte(visionTestInput), 9, 7)
			got, err := svc.prepareVisionFallback(context.Background(), c, &primary, []byte(visionTestInput))
			if status == "completed" {
				require.NoError(t, err)
				require.Contains(t, string(got), "visible details")
			} else {
				require.Error(t, err)
				require.Empty(t, got)
				require.Empty(t, svc.visionFallbackCache.entries)
			}
		})
	}
}

func TestVisionFallbackUnknownCapabilityWithoutHelperPreservesRequest(t *testing.T) {
	primary := visionTestAccount(1, "unknown-model")
	svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {primary}}}}
	body := []byte(strings.ReplaceAll(visionTestInput, "text-model", "unknown-model"))
	c, _ := visionTestContext(body, 9, 7)
	got, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, body, got)
	manifest, _, err := svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), &Group{ID: 7, Platform: PlatformOpenAI}, "")
	require.NoError(t, err)
	require.JSONEq(t, `["text"]`, gjson.GetBytes(manifest.Body, "models.0.input_modalities").Raw)
}

func TestVisionFallbackUnknownModelsAdvertiseAndForwardImages(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-6", "gpt-5.6", "gpt-reserve", "openai/gpt-oss-20b", "glm-5.3-flash", "qwen3.8-27b", "kimi-k3", "doubao-auto", "q3-4b", "mistralai/mistral-nemotron"} {
		t.Run(model, func(t *testing.T) {
			primary := visionTestAccount(1, model)
			delete(primary.Extra, UpstreamModelMetadataExtraKey)
			helper := visionTestAccount(2, "vision-model", "text", "image")
			var calls []string
			svc := &OpenAIGatewayService{cfg: visionTestConfig(),
				accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {primary, helper}}},
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					requested := gjson.GetBytes(body, "model").String()
					calls = append(calls, requested)
					if requested == "vision-model" {
						require.Contains(t, string(body), "data:image")
						return visionTestResponse(requested, "图中显示输入错误"), nil
					}
					require.Equal(t, model, requested)
					require.NotContains(t, string(body), "data:image")
					require.Contains(t, string(body), "图中显示输入错误")
					return visionTestResponse(requested, "完成"), nil
				}},
			}
			manifest, _, err := svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), &Group{ID: 7, Platform: PlatformOpenAI}, "")
			require.NoError(t, err)
			found := false
			for _, entry := range gjson.GetBytes(manifest.Body, "models").Array() {
				if entry.Get("slug").String() == model {
					found = true
					require.JSONEq(t, `["text","image"]`, entry.Get("input_modalities").Raw)
					require.False(t, entry.Get("supports_image_detail_original").Bool())
				}
			}
			require.True(t, found)
			body := []byte(strings.ReplaceAll(visionTestInput, "text-model", model))
			c, recorder := visionTestContext(body, 9, 7)
			result, err := svc.Forward(context.Background(), c, &primary, body)
			require.NoError(t, err)
			require.Equal(t, []string{"vision-model", model}, calls)
			require.Equal(t, model, result.Model)
			require.Equal(t, "完成", gjson.Get(recorder.Body.String(), "output.0.content.0.text").String())
			require.Len(t, TakeVisionFallbackUsage(c), 1)
		})
	}
}

func TestVisionFallbackMappedGPTAndNativeModelsPreserveImages(t *testing.T) {
	for _, test := range []struct {
		model      string
		upstream   string
		modalities []string
	}{
		{"custom-coder", "gpt-6-astra", []string{"text", "image"}},
		{"glm-5.3-flash", "glm-5.3-flash", []string{"text", "image"}},
	} {
		t.Run(test.model, func(t *testing.T) {
			primary := visionTestAccount(1, test.upstream, test.modalities...)
			primary.Credentials["model_mapping"] = map[string]any{test.model: test.upstream}
			body := []byte(strings.ReplaceAll(visionTestInput, "text-model", test.model))
			c, _ := visionTestContext(body, 9, 7)
			svc := &OpenAIGatewayService{cfg: visionTestConfig()}
			got, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
			require.NoError(t, err)
			require.Equal(t, body, got)
			require.Empty(t, TakeVisionFallbackUsage(c))
		})
	}
}

func TestVisionFallbackCompositeAliasUsesResolvedModel(t *testing.T) {
	primary := visionTestAccount(1, "glm-5.3-flash")
	helper := visionTestAccount(2, "vision-model", "text", "image")
	accounts := []Account{primary, helper}
	routes := []CompositeModelRoute{{PublicModel: "my-coder", TargetPlatform: PlatformOpenAI,
		UpstreamModel: "glm-5.3-flash", Endpoint: CompositeRouteEndpointResponses, Enabled: true}}
	body := []byte(`{"models":[{"slug":"my-coder","input_modalities":["text"]}]}`)
	got, err := applyVisionFallbackManifest(body, visionTestConfig(), &Group{ID: 7}, PlatformComposite, accounts, routes, true)
	require.NoError(t, err)
	require.JSONEq(t, `["text","image"]`, gjson.GetBytes(got, "models.0.input_modalities").Raw)
	got, err = applyVisionFallbackManifest(body, visionTestConfig(), &Group{ID: 7}, PlatformComposite, accounts, routes, false)
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestVisionFallbackVerifiedGPTPreservesNativeImagesWithHelperAvailable(t *testing.T) {
	primary := visionTestAccount(831, "gpt-6-astra", "text", "image")
	primary.Credentials["base_url"] = "https://www.anthropicai.cc"
	helper := visionTestAccount(2, "vision-model", "text", "image")
	svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {primary, helper}}}}
	body := []byte(strings.ReplaceAll(visionTestInput, "text-model", "gpt-6-astra"))
	c, _ := visionTestContext(body, 9, 7)
	got, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, body, got)
	require.Empty(t, TakeVisionFallbackUsage(c))
	require.False(t, groupModelNeedsVisionFallback([]Account{primary, helper}, PlatformOpenAI, "gpt-6-astra"))
}

func TestVisionFallbackPreferredHelperFailureUsesNextNativeCandidate(t *testing.T) {
	primary := visionTestAccount(1, "gpt-reserve")
	first := visionTestAccount(3, "preferred-vision", "text", "image")
	second := visionTestAccount(2, "backup-vision", "text", "image")
	cfg := visionTestConfig()
	cfg.Gateway.VisionFallback.Model = first.Name
	var calls []string
	svc := &OpenAIGatewayService{cfg: cfg,
		accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {primary, second, first}}},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			model := gjson.GetBytes(body, "model").String()
			calls = append(calls, model)
			if model == first.Name {
				return nil, fmt.Errorf("helper unavailable")
			}
			require.Equal(t, second.Name, model)
			require.Contains(t, string(body), "data:image")
			return visionTestResponse(model, "图片描述"), nil
		}},
	}
	body := []byte(strings.ReplaceAll(visionTestInput, "text-model", primary.Name))
	c, _ := visionTestContext(body, 9, 7)
	got, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, []string{first.Name, second.Name}, calls)
	require.NotContains(t, string(got), "data:image")
	require.Contains(t, string(got), "图片描述")
	require.Equal(t, primary.Name, gjson.GetBytes(got, "model").String())
	require.Len(t, TakeVisionFallbackUsage(c), 1)
}

func TestVisionFallbackHonorsPrivacyAndUsesSyncedModelsWithWildcardMapping(t *testing.T) {
	helper := visionTestAccount(2, "vision-model", "text", "image")
	helper.Credentials["model_mapping"] = map[string]any{"vision-*": "vision-model"}
	cfg := visionTestConfig()
	group := &Group{ID: 7, RequirePrivacySet: true}
	require.Empty(t, visionFallbackCandidates([]Account{helper}, cfg, group))
	helper.Extra["privacy_mode"] = PrivacyModeTrainingOff
	candidates := visionFallbackCandidates([]Account{helper}, cfg, group)
	require.Len(t, candidates, 1)
	require.Equal(t, "vision-model", candidates[0].model)
}

func TestVisionFallbackUsesSyncedOfficialModelsWithoutMappings(t *testing.T) {
	helper := visionTestAccount(2, "gpt-5.4")
	helper.Credentials = map[string]any{"api_key": "test-key", "base_url": "https://api.openai.com/v1"}
	helper.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339), Models: []string{"gpt-5.4"},
	})
	candidates := visionFallbackCandidates([]Account{helper}, visionTestConfig(), &Group{ID: 7})
	require.Len(t, candidates, 1)
	require.Equal(t, "gpt-5.4", candidates[0].model)
	// 目录只有模型 ID 时不能把第三方端点的同名模型推断为视觉模型。
	helper.Credentials["base_url"] = "https://unknown.example/v1"
	require.Empty(t, visionFallbackCandidates([]Account{helper}, visionTestConfig(), &Group{ID: 7}))
}

type visionConcurrencyCache struct {
	ConcurrencyCache
	available bool
	acquired  int
	released  int
}

func (c *visionConcurrencyCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	c.acquired++
	return c.available, nil
}

func (c *visionConcurrencyCache) ReleaseAccountSlot(context.Context, int64, string) error {
	c.released++
	return nil
}

func TestVisionFallbackConcurrencyAndSlotRelease(t *testing.T) {
	for _, tc := range []struct {
		name         string
		primaryID    int64
		available    bool
		forceAcquire bool
		wantCalls    int
		wantSlots    int
	}{
		{"different_account", 1, true, false, 1, 1},
		{"busy", 1, false, false, 0, 1},
		{"reuse_primary", 2, false, false, 1, 0},
		{"passthrough_next_turn", 2, false, true, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			helper := visionTestAccount(2, "vision-model", "text", "image")
			primary := visionTestAccount(tc.primaryID, "text-model", "text")
			capacity := &visionConcurrencyCache{available: tc.available}
			calls := 0
			svc := &OpenAIGatewayService{cfg: visionTestConfig(), concurrencyService: NewConcurrencyService(capacity),
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
					calls++
					return visionTestResponse("vision-model", "description"), nil
				}},
			}
			c, _ := visionTestContext([]byte(visionTestInput), 9, 7)
			apiKey := c.MustGet("api_key").(*APIKey)
			ctx := context.WithValue(context.Background(), visionFallbackPrimarySlotRequiredKey{}, tc.forceAcquire)
			image := visionInputImage{image: map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAAA"}}
			_, err := svc.describeVisionInput(ctx, c, apiKey, &primary, []visionFallbackCandidate{{account: &helper, model: "vision-model"}}, image, 0, 1)
			if tc.wantCalls == 0 {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.wantCalls, calls)
			require.Equal(t, tc.wantSlots, capacity.acquired)
			if tc.available {
				require.Equal(t, 1, capacity.released)
			} else {
				require.Zero(t, capacity.released)
			}
		})
	}
}
