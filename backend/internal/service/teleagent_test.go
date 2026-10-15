//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func teleagentTestAccount(t *testing.T, baseURL string) *Account {
	t.Helper()
	previous := builtinAdapterConfig.Load()
	t.Cleanup(func() { builtinAdapterConfig.Store(previous) })
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{
		Enabled: true, TeleagentURL: baseURL, TeleagentKey: "teleagent-shared-key",
	})
	return &Account{
		ID: 973, Platform: PlatformTeleagent, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "ta-" + strings.Repeat("a", 64)},
	}
}

func TestTeleagentCredentialsAndScheduling(t *testing.T) {
	account := teleagentTestAccount(t, "")
	empty := map[string]any{}
	applyBuiltinAdapterCredentials(PlatformTeleagent, empty)
	require.Equal(t, "http://sub2api-teleagent:7872/v1", empty["base_url"])
	require.NotContains(t, empty, "api_key")
	require.Error(t, validateBuiltinChatCredentials(PlatformTeleagent, AccountTypeAPIKey, empty))
	applyBuiltinAdapterCredentials(PlatformTeleagent, account.Credentials)
	require.Equal(t, "ta-"+strings.Repeat("a", 64), account.GetCredential("api_key"))
	require.NoError(t, validateBuiltinChatCredentials(PlatformTeleagent, AccountTypeAPIKey, account.Credentials))
	require.Error(t, validateBuiltinChatCredentials(PlatformTeleagent, AccountTypeOAuth, account.Credentials))
	for _, key := range []string{"teleagent-shared-key", "cloud-token", "ta-user-id", "ta-" + strings.Repeat("g", 64)} {
		require.Error(t, validateBuiltinChatCredentials(PlatformTeleagent, AccountTypeAPIKey, map[string]any{"api_key": key}))
	}
	account.Credentials["api_protocol"] = APIProtocolResponses
	require.Error(t, validateBuiltinChatCredentials(PlatformTeleagent, AccountTypeAPIKey, account.Credentials))
	require.Equal(t, APIProtocolChatCompletions, account.GetAPIProtocol())
	delete(account.Credentials, "api_protocol")
	require.True(t, account.IsOpenAICompatible())
	require.True(t, IsMultiProtocolAPIKeyProvider(PlatformTeleagent))
	require.True(t, IsAllowedQuotaPlatform(PlatformTeleagent))
	require.Equal(t, PlatformTeleagent, NormalizeOpenAICompatiblePlatform(PlatformTeleagent))
	require.Equal(t, []string{PlatformOpenAI}, MixedSchedulingTargetPlatforms(PlatformTeleagent))
	require.Contains(t, MixedSchedulingSourcePlatforms(PlatformOpenAI), PlatformTeleagent)
	require.Contains(t, schedulerSnapshotPlatforms(), PlatformTeleagent)
	require.Contains(t, modelCatalogCandidatePlatforms(PlatformOpenAI), PlatformTeleagent)
	require.Equal(t, DefaultTeleagentModelIDs(), defaultModelsListCandidateIDs(PlatformTeleagent))
	require.True(t, AutoModelTextPlatform(PlatformTeleagent))
	require.False(t, AutoModelWebAskPlatform(PlatformTeleagent))
	require.True(t, shouldForwardOpenAIResponsesViaRawChatCompletions(account))
	for _, capability := range []OpenAIEndpointCapability{OpenAIEndpointCapabilityResponses, OpenAIEndpointCapabilityEmbeddings, OpenAIEndpointCapabilityAlphaSearch, OpenAIEndpointCapabilityLive} {
		require.False(t, account.SupportsOpenAIEndpointCapability(capability))
	}
	for _, platform := range []string{PlatformTeleagent, PlatformOpenAI} {
		for _, model := range DefaultTeleagentModelIDs() {
			require.True(t, isOpenAICompatibleAccountEligibleForRequest(context.Background(), account, platform, model, false, OpenAIEndpointCapabilityChatCompletions))
		}
	}
	require.False(t, isOpenAICompatibleAccountEligibleForRequest(context.Background(), account, PlatformOpenAI, "gpt-unrelated", false, OpenAIEndpointCapabilityChatCompletions))
	require.False(t, openAIAccountMatchesPlatform(account, PlatformAnthropic))
	messagesCtx := WithOpenAIRequestProtocol(context.Background(), OpenAIRequestProtocolMessages)
	require.False(t, isOpenAICompatibleAccountEligibleForRequest(messagesCtx, account, PlatformOpenAI, DefaultTeleagentModel, false, OpenAIEndpointCapabilityChatCompletions))
	require.True(t, ModelAccountCompatible(account, DefaultTeleagentModel, []byte(`{"tools":[{"type":"function","function":{"name":"lookup"}}],"max_tokens":128}`)))
	for _, model := range append(DefaultTeleagentModelIDs(), "teleagent/chat-pro") {
		platform, ok := DetectModelPlatform(model)
		require.True(t, ok)
		require.Equal(t, PlatformTeleagent, platform)
	}
}

func TestTeleagentBulkRejectsSharedKeyBeforeWriting(t *testing.T) {
	account := teleagentTestAccount(t, "")
	repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*Account{account}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{account.ID}, Credentials: map[string]any{"api_key": "teleagent-shared-key"},
	})
	require.Error(t, err)
	require.Equal(t, 0, repo.bulkUpdateCalls)
	require.Empty(t, repo.updatedAccounts)
}

func TestTeleagentSharedKeyBoundToTarget(t *testing.T) {
	teleagentTestAccount(t, "https://adapter.internal/teleagent/v1")
	for _, tc := range []struct {
		target string
		want   string
	}{
		{"https://adapter.internal/teleagent/v1/chat/completions", "teleagent-shared-key"},
		{"https://adapter.internal/teleagent/v1/models", "teleagent-shared-key"},
		{"https://other.internal/teleagent/v1/chat/completions", ""},
		{"http://adapter.internal/teleagent/v1/models", ""},
		{"https://adapter.internal:443/teleagent/v1/models", ""},
		{"https://adapter.internal/teleagent/v10/models", ""},
		{"https://adapter.internal/other/v1/models", ""},
		{"https://adapter.internal/teleagent/v1/../../other/models", ""},
	} {
		require.Equal(t, tc.want, builtinAdapterSharedKeyForTarget(PlatformTeleagent, tc.target), tc.target)
	}
}

func TestTeleagentChatAndResponsesUseDualAuthentication(t *testing.T) {
	for _, custom := range []bool{false, true} {
		for _, responses := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{true: "custom", false: "builtin"}[custom], map[bool]string{true: "responses", false: "chat"}[responses]}, "/"), func(t *testing.T) {
				account := teleagentTestAccount(t, "http://teleagent.internal:7872")
				if custom {
					account.Credentials["base_url"] = "http://custom.internal/v1"
				}
				body := []byte(`{"model":"chat-pro","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`)
				path := "/v1/chat/completions"
				if responses {
					path = "/v1/responses"
					body = []byte(`{"model":"chat-pro","input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`)
				}
				upstream := &httpUpstreamRecorder{resp: newJSONResponse(http.StatusOK, `{"id":"chatcmpl-ta","model":"chat-pro","choices":[{"index":0,"message":{"role":"assistant","content":"ok","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				c := adaptiveProtocolTestContext(path, body)
				var err error
				if responses {
					_, err = svc.Forward(context.Background(), c, account, body)
				} else {
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				}
				require.NoError(t, err)
				require.Equal(t, "/v1/chat/completions", upstream.lastReq.URL.Path)
				require.Equal(t, "Bearer "+account.GetCredential("api_key"), upstream.lastReq.Header.Get("Authorization"))
				wantSharedKey := "teleagent-shared-key"
				if custom {
					wantSharedKey = ""
				}
				require.Equal(t, wantSharedKey, upstream.lastReq.Header.Get("X-Sub2API-Adapter-Key"))
				require.Equal(t, "lookup", gjson.GetBytes(upstream.lastBody, "tools.0.function.name").String())
			})
		}
	}
}

func TestTeleagentStreamPreservesToolCalls(t *testing.T) {
	for _, responses := range []bool{false, true} {
		account := teleagentTestAccount(t, "http://teleagent.internal:7872")
		body := []byte(`{"model":"chat-pro","messages":[{"role":"user","content":"hello"}],"stream":true,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`)
		path := "/v1/chat/completions"
		if responses {
			path = "/v1/responses"
			body = []byte(`{"model":"chat-pro","input":"hello","stream":true,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`)
		}
		stream := "data: {\"id\":\"chatcmpl-ta\",\"model\":\"chat-pro\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl-ta\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n"
		upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}}
		svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
		var err error
		if responses {
			_, err = svc.Forward(context.Background(), c, account, body)
		} else {
			_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
		}
		require.NoError(t, err)
		require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
		require.Equal(t, "lookup", gjson.GetBytes(upstream.lastBody, "tools.0.function.name").String())
		require.Equal(t, "teleagent-shared-key", upstream.lastReq.Header.Get("X-Sub2API-Adapter-Key"))
		require.Contains(t, recorder.Body.String(), "lookup")
		if responses {
			require.Contains(t, recorder.Body.String(), "response.completed")
			require.Contains(t, recorder.Body.String(), "function_call")
		} else {
			require.Contains(t, recorder.Body.String(), "[DONE]")
			require.Contains(t, recorder.Body.String(), "tool_calls")
		}
	}
}

func TestTeleagentAccountTestAndModelsUseDualAuthentication(t *testing.T) {
	for _, custom := range []bool{false, true} {
		account := teleagentTestAccount(t, "http://teleagent.internal:7872")
		wantSharedKey := "teleagent-shared-key"
		if custom {
			account.Credentials["base_url"] = "http://custom.internal/v1"
			wantSharedKey = ""
		}
		svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse(), newJSONResponse(http.StatusOK, `{"data":[{"id":"chat-pro"},{"id":"chat-flash"}]}`))
		c, recorder := newTestContext()
		require.NoError(t, svc.TestAccountConnection(c, account.ID, "", "hello", AccountTestModeDefault))
		require.Equal(t, DefaultTeleagentModel, gjson.GetBytes(upstream.lastBody, "model").String())
		require.Contains(t, recorder.Body.String(), `"success":true`)
		models, err := svc.FetchUpstreamSupportedModels(context.Background(), account)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"chat-pro", "chat-flash"}, models)
		require.Len(t, upstream.requests, 2)
		for _, req := range upstream.requests {
			require.Equal(t, "Bearer "+account.GetCredential("api_key"), req.Header.Get("Authorization"))
			require.Equal(t, wantSharedKey, req.Header.Get("X-Sub2API-Adapter-Key"))
		}
	}
}

func TestTeleagentBrowserLoginProxyAndScope(t *testing.T) {
	sessionID := strings.Repeat("b", 64)
	localKey := "ta-" + strings.Repeat("c", 64)
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer teleagent-shared-key", r.Header.Get("Authorization"))
		if r.Header.Get("X-Login-Owner") != "user:7" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		require.True(t, strings.HasPrefix(r.URL.Path, "/internal/login/sessions"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/view"):
			require.Equal(t, http.MethodGet, r.Method)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-fixture"))
		case strings.HasSuffix(r.URL.Path, "/input"):
			require.Equal(t, http.MethodPost, r.Method)
			var event BuiltinLoginInputEvent
			require.NoError(t, json.NewDecoder(r.Body).Decode(&event))
			require.Equal(t, "click", event.Type)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			status := "pending"
			if strings.HasSuffix(r.URL.Path, "/poll") {
				status = "completed"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"session_id": sessionID, "status": status, "mode": "poll", "api_key": localKey, "token": "cloud-secret", "account": map[string]any{"uid": "7", "nickname": "TeleAgent user", "token": "cloud-secret"}})
		}
	}))
	defer adapter.Close()
	teleagentTestAccount(t, adapter.URL+"/v1")
	ctx := context.Background()
	start, err := BuiltinAdapterLogin(ctx, PlatformTeleagent, "user:7", "", "start", "")
	require.NoError(t, err)
	require.Equal(t, "poll", start.Mode)
	require.Empty(t, start.APIKey)
	result, err := BuiltinAdapterLogin(ctx, PlatformTeleagent, "user:7", sessionID, "poll", "")
	require.NoError(t, err)
	require.Equal(t, localKey, result.APIKey)
	require.Equal(t, "7", result.Account.UID)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "cloud-secret")
	require.NotContains(t, string(raw), "teleagent-shared-key")
	view, err := BuiltinAdapterLoginView(ctx, PlatformTeleagent, "user:7", sessionID)
	require.NoError(t, err)
	require.Equal(t, "image/png", view.ContentType)
	require.NoError(t, BuiltinAdapterLoginInput(ctx, PlatformTeleagent, "user:7", sessionID, BuiltinLoginInputEvent{Type: "click", X: 12, Y: 34}))
	for _, owner := range []string{"user:8", "admin:7"} {
		_, err = BuiltinAdapterLogin(ctx, PlatformTeleagent, owner, sessionID, "poll", "")
		require.Error(t, err)
		_, err = BuiltinAdapterLoginView(ctx, PlatformTeleagent, owner, sessionID)
		require.Error(t, err)
		require.Error(t, BuiltinAdapterLoginInput(ctx, PlatformTeleagent, owner, sessionID, BuiltinLoginInputEvent{Type: "click"}))
	}
	_, err = BuiltinAdapterLoginView(ctx, PlatformOpenAI, "user:7", sessionID)
	require.Error(t, err)
	require.Error(t, BuiltinAdapterLoginInput(ctx, PlatformOpenAI, "user:7", sessionID, BuiltinLoginInputEvent{Type: "click"}))
	cancelled, err := BuiltinAdapterLogin(ctx, PlatformTeleagent, "user:7", sessionID, "cancel", "")
	require.NoError(t, err)
	require.Equal(t, "cancelled", cancelled.Status)
}

func TestTeleagentLoginRejectsCloudTokenAsAccountKey(t *testing.T) {
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"session_id":"session","status":"completed","mode":"poll","api_key":"cloud-token","account":{"uid":"7"}}`))
	}))
	defer adapter.Close()
	teleagentTestAccount(t, adapter.URL)
	_, err := BuiltinAdapterLogin(context.Background(), PlatformTeleagent, "user:7", "", "start", "")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "cloud-token")
}
