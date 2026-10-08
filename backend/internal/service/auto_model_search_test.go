package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const verifiedSearchResponse = `{"id":"resp_search","model":"search-model","object":"response","status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"type":"search","query":"documentation"}},{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"The API documentation describes the Responses endpoint.","annotations":[{"type":"url_citation","url":"https://example.com/docs","title":"Documentation"}]}]}],"usage":{"input_tokens":20,"output_tokens":30,"total_tokens":50}}`

func searchTestRoutingContext(t *testing.T, body []byte, accounts []Account) context.Context {
	ctx := context.WithValue(context.Background(), autoModelRoutingPolicyContextKey{}, &autoModelRoutingPolicy{policy: &AutoModelPolicy{}})
	ctx = context.WithValue(ctx, autoModelAccountsKey{}, &autoModelInventory{groupID: 7, accounts: accounts})
	ctx = WithAutoModelRequestCapabilities(ctx, body)
	ctx, err := (&GatewayService{}).BindAutoModelSearchCapabilities(ctx, &Group{ID: 7, Platform: PlatformOpenAI}, body)
	require.NoError(t, err)
	return ctx
}

func searchTestHelper() Account {
	helper := visionTestAccount(2, "search-model", "text")
	helper.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceResponses)
	return helper
}

func TestAutoSearchFallbackKeepsPrimaryAndBillsHelperOnce(t *testing.T) {
	primary, helper := visionTestAccount(1, "text-model", "text"), searchTestHelper()
	body := []byte(`{"model":"text-model","input":"Find the official API docs","tools":[{"type":"web_search","filters":{"allowed_domains":["example.com"]}},{"type":"function","name":"shell","parameters":{"type":"object"}}],"stream":false}`)
	ctx := searchTestRoutingContext(t, body, []Account{primary, helper})
	var calls []int64
	svc := &OpenAIGatewayService{cfg: visionTestConfig(),
		accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {primary, helper}}},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
			calls = append(calls, id)
			forwarded, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			if id == helper.ID {
				require.Equal(t, "/v1/responses", req.URL.Path)
				require.Equal(t, "example.com", gjson.GetBytes(forwarded, "tools.0.filters.allowed_domains.0").String())
				require.NotContains(t, string(forwarded), `"shell"`)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(verifiedSearchResponse))}, nil
			}
			require.Equal(t, primary.ID, id)
			require.Equal(t, "/v1/chat/completions", req.URL.Path)
			require.Contains(t, string(forwarded), "https://example.com/docs")
			require.Contains(t, string(forwarded), "untrusted source material")
			require.NotContains(t, string(forwarded), `"type":"web_search"`)
			require.Contains(t, string(forwarded), `"shell"`)
			return visionTestResponse(primary.Name, "Combined answer from the primary model"), nil
		}},
	}
	c, recorder := visionTestContext(body, 9, 7)
	c.Request = c.Request.WithContext(ctx)
	result, err := svc.Forward(ctx, c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, primary.Name, result.Model)
	require.Contains(t, recorder.Body.String(), "Combined answer")
	require.Equal(t, []int64{2, 1}, calls)
	usage := TakeVisionFallbackUsage(c)
	require.Len(t, usage, 1)
	require.Equal(t, helper.ID, usage[0].Account.ID)
	require.True(t, strings.HasPrefix(usage[0].Result.RequestID, "search_helper:"))
	require.NotEmpty(t, usage[0].PayloadHash)
	_, err = svc.prepareSearchFallback(ctx, c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, []int64{2, 1}, calls)
	require.Empty(t, TakeVisionFallbackUsage(c))
}

func TestAutoSearchAdmissionRequiresGroupHelper(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	body := []byte(`{"model":"auto","input":"Find API docs","tools":[{"type":"web_search"}]}`)
	for _, available := range []bool{false, true} {
		helper := searchTestHelper()
		helper.Schedulable = available
		ctx := searchTestRoutingContext(t, body, []Account{primary, helper})
		require.Equal(t, available, AutoModelRequestAccountCompatible(ctx, &primary, primary.Name, body))
		require.False(t, ModelAccountCompatible(&primary, primary.Name, body))
		require.False(t, ModelFallbackAccountCompatible(&primary, primary.Name, body))
	}
	helper := searchTestHelper()
	group := &Group{ID: 7, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"auto", primary.Name}}}
	require.Empty(t, searchFallbackCandidates(context.Background(), []Account{helper}, group, body))
	helper.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceChatCompletions)
	require.Empty(t, searchFallbackCandidates(context.Background(), []Account{helper}, nil, body))
	for _, input := range []string{
		`{"input":"Find docs","tool_choice":"none","tools":[{"type":"web_search"}]}`,
		`{"input":[{"type":"function_call_output","output":"Find docs"}],"tools":[{"type":"web_search"}]}`,
	} {
		ctx := searchTestRoutingContext(t, []byte(input), []Account{primary, searchTestHelper()})
		require.False(t, AutoModelRequestAccountCompatible(ctx, &primary, primary.Name, []byte(input)))
	}
}

func TestDelegatedSearchPreservesHistoryAndClientFunctions(t *testing.T) {
	body := []byte(`{"model":"primary","previous_response_id":"resp_prev","tool_choice":{"type":"function","name":"shell"},"tools":[{"type":"namespace","name":"helpers","tools":[{"type":"web_search_preview"},{"type":"function","name":"shell"}]}],"input":[{"type":"additional_tools","tools":[{"type":"web_search"}]},{"type":"function_call_output","call_id":"c1","output":"keep this"}]}`)
	converted, err := stripDelegatedSearchTools(body)
	require.NoError(t, err)
	require.False(t, modelRequestNeedsNativeSearchTools(converted))
	for _, path := range []string{"model", "previous_response_id", "tool_choice", "input.1"} {
		original, updated := gjson.GetBytes(body, path), gjson.GetBytes(converted, path)
		if original.IsObject() {
			require.JSONEq(t, original.Raw, updated.Raw)
		} else {
			require.Equal(t, original.Raw, updated.Raw)
		}
	}
	require.Equal(t, "shell", gjson.GetBytes(converted, "tools.0.tools.0.name").String())
	choice, err := stripDelegatedSearchTools([]byte(`{"tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search"}}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(choice, "tool_choice").Exists())
}

func TestSearchHelperRequiresCompletedSearchWithSources(t *testing.T) {
	text, err := verifiedSearchFallbackText([]byte(verifiedSearchResponse))
	require.NoError(t, err)
	require.Contains(t, text, "https://example.com/docs")
	for _, response := range []string{
		strings.ReplaceAll(verifiedSearchResponse, `"type":"web_search_call"`, `"type":"reasoning"`),
		strings.ReplaceAll(verifiedSearchResponse, `"type":"url_citation"`, `"type":"unsupported"`),
		strings.ReplaceAll(verifiedSearchResponse, `"status":"completed"`, `"status":"incomplete"`),
	} {
		_, err := verifiedSearchFallbackText([]byte(response))
		require.Error(t, err)
	}
}

func TestSearchFallbackRejectsPlainAnswerAndCachesFailure(t *testing.T) {
	primary, helper := visionTestAccount(1, "text-model", "text"), searchTestHelper()
	body := []byte(`{"model":"text-model","input":"Find official documentation","tools":[{"type":"web_search"}]}`)
	ctx := searchTestRoutingContext(t, body, []Account{primary, helper})
	calls := 0
	svc := &OpenAIGatewayService{cfg: visionTestConfig(),
		accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {primary, helper}}},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
			calls++
			require.Equal(t, helper.ID, id, "没有真实搜索证据时不得继续调用主模型")
			plain := strings.ReplaceAll(verifiedSearchResponse, `"type":"web_search_call"`, `"type":"reasoning"`)
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(plain))}, nil
		}},
	}
	c, recorder := visionTestContext(body, 9, 7)
	for attempt := 0; attempt < 2; attempt++ {
		_, err := svc.prepareSearchFallback(ctx, c, &primary, body)
		require.Error(t, err)
	}
	require.Equal(t, 1, calls)
	require.Empty(t, recorder.Body.String())
	require.Len(t, TakeVisionFallbackUsage(c), 1, "辅助产生的用量在主调用失败时仍需记录")
	nativeBody, err := svc.prepareSearchFallback(ctx, c, &helper, body)
	require.NoError(t, err)
	require.Equal(t, body, nativeBody)
}

func TestSearchFallbackDoesNotRepeatAcrossToolContinuationRequests(t *testing.T) {
	primary, helper := visionTestAccount(1, "text-model", "text"), searchTestHelper()
	svc := &OpenAIGatewayService{cfg: visionTestConfig(),
		accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {primary, helper}}},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
			t.Fatal("工具续轮不应重新执行辅助搜索")
			return nil, nil
		}},
	}
	for _, output := range []string{"read file contents", "command finished"} {
		body := []byte(`{"model":"text-model","tools":[{"type":"web_search"},{"type":"function","name":"shell","parameters":{"type":"object"}}],"input":[{"role":"user","content":"定位 429 请求对应模型的降级路径"},{"type":"function_call","call_id":"c1","name":"shell","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"` + output + `"}]}`)
		ctx := searchTestRoutingContext(t, body, []Account{primary, helper})
		c, _ := visionTestContext(body, 9, 7)
		converted, err := svc.prepareSearchFallback(ctx, c, &primary, body)
		require.NoError(t, err)
		require.False(t, modelRequestNeedsNativeSearchTools(converted))
		require.JSONEq(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(converted, "input").Raw)
		require.Equal(t, "shell", gjson.GetBytes(converted, "tools.0.name").String())
		require.Empty(t, TakeVisionFallbackUsage(c))
		nativeBody, err := svc.prepareSearchFallback(ctx, c, &helper, body)
		require.NoError(t, err)
		require.Equal(t, body, nativeBody)
	}
}

func TestSearchFallbackNewUserAndForcedSearchStillRun(t *testing.T) {
	for _, body := range []string{
		`{"input":"Find current API docs"}`,
		`{"input":[{"type":"function_call_output","output":"done"},{"role":"user","content":"查官方文档"},{"type":"additional_tools","tools":[]}]}`,
		`{"input":[{"type":"function_call_output","output":"done"}],"tool_choice":"required"}`,
		`{"input":[{"type":"function_call_output","output":"done"}],"tool_choice":{"type":"web_search_preview"}}`,
	} {
		require.True(t, shouldRunSearchFallback([]byte(body)), body)
	}
	for _, body := range []string{
		`{"input":[{"role":"user","content":"fix code"},{"role":"assistant","content":"reading"}]}`,
		`{"input":[{"role":"user","content":"fix code"},{"type":"function_call_output","output":"done"},{"type":"additional_tools","tools":[]}]}`,
	} {
		require.False(t, shouldRunSearchFallback([]byte(body)), body)
	}
}

func TestAutoSearchOfficialDeepSeekRequiresHelper(t *testing.T) {
	helper := searchTestHelper()
	body := []byte(`{"input":"Find API docs","tools":[{"type":"web_search"}]}`)
	helper.Credentials["base_url"] = "https://api.deepseek.com"
	require.False(t, modelAccountPreservesSearchTools(&helper, helper.Name, body))
	require.Empty(t, searchFallbackCandidates(context.Background(), []Account{helper}, nil, body))
	helper.Credentials["base_url"] = "https://third-party.example.com"
	require.True(t, modelAccountPreservesSearchTools(&helper, helper.Name, body))
}

func TestAutoSearchStreamsOnlyPrimaryResponse(t *testing.T) {
	primary, helper := visionTestAccount(1, "text-model", "text"), searchTestHelper()
	body := []byte(`{"model":"text-model","input":"Find API docs","tools":[{"type":"web_search"}],"stream":true}`)
	ctx := searchTestRoutingContext(t, body, []Account{primary, helper})
	var calls []int64
	svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {primary, helper}}},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
			calls = append(calls, id)
			forwarded, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			if id == helper.ID {
				require.False(t, gjson.GetBytes(forwarded, "stream").Bool())
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(verifiedSearchResponse))}, nil
			}
			require.True(t, gjson.GetBytes(forwarded, "stream").Bool())
			require.Contains(t, string(forwarded), "https://example.com/docs")
			events := "data: {\"id\":\"chat-main\",\"model\":\"text-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"final answer\"},\"finish_reason\":null}]}\n\n" +
				"data: {\"id\":\"chat-main\",\"model\":\"text-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\n" + "data: [DONE]\n\n"
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(events))}, nil
		}}}
	c, recorder := visionTestContext(body, 9, 7)
	result, err := svc.Forward(ctx, c, &primary, body)
	require.NoError(t, err)
	require.True(t, result.Stream)
	require.Equal(t, []int64{2, 1}, calls)
	require.Contains(t, recorder.Body.String(), "response.completed")
	require.Contains(t, recorder.Body.String(), "final answer")
	require.NotContains(t, recorder.Body.String(), "The API documentation describes")
	require.NotContains(t, recorder.Body.String(), "resp_search")
	require.Len(t, TakeVisionFallbackUsage(c), 1)
}
