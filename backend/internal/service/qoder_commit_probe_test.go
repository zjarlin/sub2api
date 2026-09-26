package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestQoderCommitMessageRequestBodyMatchesClientContract(t *testing.T) {
	body := QoderCommitMessageRequestBody(
		"claude-sonnet-4-5",
		"diff --git a/x b/x\n+hello",
		"req-1",
		"sess-1",
		"set-1",
		"qoder-ide",
	)

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}

	if got["model"] != "claude-sonnet-4-5" {
		t.Fatalf("model = %v", got["model"])
	}
	if got["stream"] != true {
		t.Fatalf("stream = %v, want true", got["stream"])
	}
	streamOptions, ok := got["stream_options"].(map[string]any)
	if !ok || streamOptions["include_usage"] != true {
		t.Fatalf("stream_options = %v", got["stream_options"])
	}

	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %v", got["messages"])
	}
	system, _ := messages[0].(map[string]any)
	if system["role"] != "system" {
		t.Fatalf("first message role = %v", system["role"])
	}
	user, _ := messages[1].(map[string]any)
	userContent, _ := user["content"].(string)
	if !strings.Contains(userContent, "diff --git a/x b/x") {
		t.Fatalf("user content missing diff: %q", userContent)
	}

	metadata, ok := got["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %v", got["metadata"])
	}
	context, ok := metadata["context"].(map[string]any)
	if !ok {
		t.Fatalf("metadata.context = %v", metadata["context"])
	}
	if context["request_id"] != "req-1" || context["session_id"] != "sess-1" || context["request_set_id"] != "set-1" {
		t.Fatalf("metadata.context ids = %v", context)
	}
	if context["client_type"] != "qoder-ide" {
		t.Fatalf("metadata.context.client_type = %v", context["client_type"])
	}
}

func TestQoderChatCompletionsURLUsesModelV1Path(t *testing.T) {
	if got := QoderChatCompletionsURL(); !strings.HasSuffix(got, "/model/v1/chat/completions") {
		t.Fatalf("QoderChatCompletionsURL() = %q", got)
	}
}

// Qoder 的 base URL 已带 /model/v1，转发层拼接 chat completions 时不能重复 /v1。
func TestQoderForwardingURLDoesNotDuplicateVersionSegment(t *testing.T) {
	if got := buildOpenAIChatCompletionsURL(QoderModelServerURL()); got != QoderChatCompletionsURL() {
		t.Fatalf("buildOpenAIChatCompletionsURL(QoderModelServerURL()) = %q, want %q", got, QoderChatCompletionsURL())
	}
}

func TestQoderCommitMessageHeadersCarryClientIdentity(t *testing.T) {
	headers := QoderCommitMessageHeaders("req-1", "sess-1")
	if headers["Accept"] != "text/event-stream" {
		t.Fatalf("Accept = %q", headers["Accept"])
	}
	if headers["X-Request-ID"] != "req-1" || headers["X-Session-ID"] != "sess-1" {
		t.Fatalf("identity headers = %v", headers)
	}
}

func TestQoderCommitMessageProbeStreamsGeneratedMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var captured *http.Request
	var capturedBody []byte
	upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		captured = req
		capturedBody, _ = io.ReadAll(req.Body)
		body := strings.Join([]string{
			"data: {\"choices\":[{\"delta\":{\"content\":\"feat(api): \"}}]}",
			"",
			"data: {\"choices\":[{\"delta\":{\"content\":\"add qoder probe\"}}]}",
			"",
			"data: [DONE]",
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/admin/accounts/1/test", nil)

	service := &AccountTestService{httpUpstream: upstream}
	if err := service.testQoderCommitMessageConnection(ginCtx, &Account{
		ID:       1,
		Platform: PlatformQoder,
		Credentials: map[string]any{
			"api_key": "qoder-token",
		},
	}, "claude-sonnet-4-5", "diff --git a/x b/x\n+hello"); err != nil {
		t.Fatalf("probe error: %v", err)
	}

	if captured == nil {
		t.Fatal("upstream was not called")
	}
	if captured.URL.Path != "/model/v1/chat/completions" {
		t.Fatalf("upstream path = %q", captured.URL.Path)
	}
	if captured.Header.Get("Authorization") != "Bearer qoder-token" {
		t.Fatalf("authorization = %q", captured.Header.Get("Authorization"))
	}
	if captured.Header.Get("X-Request-ID") == "" || captured.Header.Get("X-Session-ID") == "" {
		t.Fatalf("missing client identity headers: %v", captured.Header)
	}
	if !strings.Contains(string(capturedBody), "\"stream\":true") || !strings.Contains(string(capturedBody), "include_usage") {
		t.Fatalf("request body missing Qoder stream contract: %s", capturedBody)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, "feat(api): ") || !strings.Contains(body, "add qoder probe") {
		t.Fatalf("stream missing generated commit message: %s", body)
	}
	if !strings.Contains(body, "test_complete") {
		t.Fatalf("stream missing completion event: %s", body)
	}
}

func TestValidateQoderCredentialsRequiresAccessToken(t *testing.T) {
	if err := validateQoderCredentials(PlatformQoder, AccountTypeAPIKey, map[string]any{"api_key": "token"}); err != nil {
		t.Fatalf("valid qoder credentials rejected: %v", err)
	}
	if err := validateQoderCredentials(PlatformQoder, AccountTypeAPIKey, map[string]any{}); err == nil {
		t.Fatal("missing qoder access token was accepted")
	}
	// 设备流 OAuth 账号：设备令牌存 access_token，允许 api_key 作为兼容回退。
	if err := validateQoderCredentials(PlatformQoder, AccountTypeOAuth, map[string]any{"access_token": "device"}); err != nil {
		t.Fatalf("valid qoder oauth credentials rejected: %v", err)
	}
	if err := validateQoderCredentials(PlatformQoder, AccountTypeOAuth, map[string]any{}); err == nil {
		t.Fatal("missing qoder oauth device token was accepted")
	}
	if err := validateQoderCredentials(PlatformQoder, "service_account", map[string]any{"access_token": "device"}); err == nil {
		t.Fatal("unsupported qoder account type was accepted")
	}
}

func TestQoderCommitMessageModeRoutesToProbe(t *testing.T) {
	if got := normalizeAccountTestMode(AccountTestModeQoderCommitMessage); got != AccountTestModeQoderCommitMessage {
		t.Fatalf("normalizeAccountTestMode(commit-message) = %q", got)
	}
	if account := (&Account{Platform: PlatformQoder}); !account.IsQoder() {
		t.Fatal("IsQoder() = false for qoder platform")
	}
}
