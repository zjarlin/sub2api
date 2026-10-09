package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const askTestStream = ":heartbeat\n\n" +
	"data: {\"type\":\"answer\",\"text\":\"metadata\"}\n\n" +
	"data: {\"type\":\"stage\",\"stage\":[\"planning\"],\"text\":\"metadata\"}\n\n" +
	"data: {\"delta\":{\"reasoning_content\":\"private reasoning\"}}\n\n" +
	"data: {\"delta\":{\"content\":\"你好\"}}\n\n" +
	"data: {\"delta\":{\"content\":\" \"}}\n\n" +
	"data: {\"delta\":{\"content\":\"世界\",\"reasoning_content\":\"private reasoning\"}}\n\n" +
	"data: {\"text\":\"你好 世界\"}\n\n" +
	"data: {\"text\":\"[DONE]\",\"completion_tokens\":2}\n\n"

func askTestCredential() credential {
	return credential{
		UID: "test-user-id", Nickname: "fallback-name", Cftk: "browser-csrf",
		Cookies: []cookiePair{{Name: "sso", Value: "browser-cookie"}},
		IAM: &iamGrant{
			UserName:    "native-user-name",
			Credentials: iamCredentials{AccessKeyID: "test-ak", SecretAccessKey: "test-sk", SecurityToken: "test-security-token"},
		},
	}
}

func askTestAdapter(origin string) *adapter {
	short, stream := newHTTPClients()
	return &adapter{askOrigin: origin, client: short, streamClient: stream}
}

func assertAskError(t *testing.T, err error, code string) {
	t.Helper()
	var upstream *upstreamError
	if !errors.As(err, &upstream) || upstream.code != code {
		t.Fatalf("expected error code %s, got %v", code, err)
	}
	for _, private := range []string{"test-ak", "test-sk", "test-security-token", "browser-cookie", "Signature="} {
		if strings.Contains(err.Error(), private) {
			t.Fatal("upstream error exposed authentication material")
		}
	}
}

// 从服务端收到的字节独立重算 HMAC，避免客户端与测试共享签名构造错误。
func verifyAskTestSignature(t *testing.T, r *http.Request, body []byte) {
	t.Helper()
	if r.Header.Get("Cookie") != "" || r.Header.Get("cftk") != "" || r.Header.Get("X-Security-Token") != "test-security-token" {
		t.Error("native Ask must use temporary IAM credentials without browser cookies")
	}
	if _, err := time.Parse("20060102T150405Z", r.Header.Get("X-Sdk-Date")); err != nil {
		t.Error("missing valid SDK signing timestamp")
	}
	authorization := r.Header.Get("Authorization")
	const prefix = "SDK-HMAC-SHA256 Access=test-ak, SignedHeaders="
	if !strings.HasPrefix(authorization, prefix) {
		t.Error("missing native SDK authorization")
		return
	}
	signed, signature, ok := strings.Cut(strings.TrimPrefix(authorization, prefix), ", Signature=")
	if !ok || !strings.Contains(";"+signed+";", ";host;") || !strings.Contains(";"+signed+";", ";x-security-token;") {
		t.Error("required IAM headers were not signed")
		return
	}
	names := strings.Split(signed, ";")
	if !sort.StringsAreSorted(names) {
		t.Error("signature headers are not canonical")
	}
	var headers strings.Builder
	for _, name := range names {
		value := r.Header.Get(name)
		if name == "host" {
			value = r.Host
		}
		fmt.Fprintf(&headers, "%s:%s\n", name, strings.TrimSpace(value))
	}
	escape := func(value string) string { return strings.ReplaceAll(url.QueryEscape(value), "+", "%20") }
	parts := strings.Split(r.URL.Path, "/")
	for index, part := range parts {
		parts[index] = escape(part)
	}
	path := strings.Join(parts, "/")
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	query := r.URL.Query()
	var keys []string
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var queryParts []string
	for _, key := range keys {
		values := query[key]
		sort.Strings(values)
		for _, value := range values {
			queryParts = append(queryParts, escape(key)+"="+escape(value))
		}
	}
	canonical := strings.Join([]string{
		r.Method, path, strings.Join(queryParts, "&"), headers.String(), signed, fmt.Sprintf("%x", sha256.Sum256(body)),
	}, "\n")
	toSign := "SDK-HMAC-SHA256\n" + r.Header.Get("X-Sdk-Date") + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	mac := hmac.New(sha256.New, []byte("test-sk"))
	_, _ = io.WriteString(mac, toSign)
	if hex.EncodeToString(mac.Sum(nil)) != signature {
		t.Error("signature does not match the received HTTP body and query")
	}
}

func TestAskCompletionProtocol(t *testing.T) {
	for _, callback := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "callback"}[callback], func(t *testing.T) {
			prompt := "[系统指令] 保留历史\n\n[用户] " + strings.Repeat("长", 520)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/chat" || r.URL.RawQuery != "" {
					t.Error("unexpected Ask route")
				}
				body, _ := io.ReadAll(r.Body)
				verifyAskTestSignature(t, r, body)
				if r.Header.Get("Agent-Type") != "ChatAgent" || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("X-Language") != "zh-cn" {
					t.Error("missing native chat routing headers")
				}
				var requestBody map[string]any
				if json.Unmarshal(body, &requestBody) != nil {
					t.Error("invalid native request body")
				}
				if len(requestBody) != 11 || requestBody["client"] != "IDE" || requestBody["task"] != "chat" ||
					requestBody["user_id"] != "native-user-name" || requestBody["model_id"] != "GLM-5.2" ||
					requestBody["attempt"] != float64(1) || requestBody["is_delta_response"] != true {
					t.Error("unexpected native Ask fields")
				}
				if !reflect.DeepEqual(requestBody["messages"], []any{map[string]any{"type": "text", "content": prompt}}) ||
					!reflect.DeepEqual(requestBody["task_parameters"], map[string]any{"ide": "Visual Studio Code", "enable_code_interpreter": false}) ||
					!reflect.DeepEqual(requestBody["batch_task_parameters"], []any{}) {
					t.Error("Ask request must contain only the complete text prompt and native text settings")
				}
				if requestBody["user_prompt"] != string([]rune(prompt)[:500]) {
					t.Error("prompt preview must be bounded without truncating the actual message")
				}
				chatID, _ := requestBody["chat_id"].(string)
				if raw, err := hex.DecodeString(chatID); err != nil || len(raw) != 16 {
					t.Error("chat id must be a 32 digit hexadecimal UUID")
				}
				w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
				_, _ = io.WriteString(w, askTestStream)
			}))
			defer upstream.Close()
			var chunks []string
			var onToken func(string) error
			if callback {
				onToken = func(chunk string) error { chunks = append(chunks, chunk); return nil }
			}
			result, err := askTestAdapter(upstream.URL).askCompletion(context.Background(), askTestCredential(), prompt, "GLM-5.2", onToken)
			if err != nil || result.Text != "你好 世界" || calls.Load() != 1 {
				t.Fatalf("unexpected Ask result: %q, %v, calls=%d", result.Text, err, calls.Load())
			}
			if callback && !reflect.DeepEqual(chunks, []string{"你好", " ", "世界"}) {
				t.Fatalf("unexpected visible chunks: %#v", chunks)
			}
		})
	}
}

func TestAskSignedQueryEncoding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifyAskTestSignature(t, r, nil)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	a := askTestAdapter(upstream.URL)
	req, err := a.askRequest(context.Background(), askTestCredential(), http.MethodGet, "/v1/model/builtin?z=last&x=a+b&x=%2F&x=%E4%B8%AD", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
}

func TestAskEventStreamFramesAndFailures(t *testing.T) {
	tests := []struct {
		name, stream, text, code string
	}{
		{"native deltas", askTestStream, "你好 世界", ""},
		{"multiline CRLF", "data: {\r\ndata: \"delta\":{\"content\":\"你好\"}}\r\n\r\ndata: {\"text\":\"[DONE]\"}", "你好", ""},
		{"cumulative text", "data: {\"text\":\"hello\"}\n\ndata: {\"text\":\"hello\"}\n\ndata: {\"text\":\"hello world\"}\n\ndata: {\"text\":\"hello\"}\n\ndata: {\"delta\":{\"content\":\"!\"}}\n\ndata: {\"text\":\"hello world!\"}\n\ndata: {\"text\":\"[DONE]\"}\n\n", "hello world!", ""},
		{"success codes", "data: {\"error_code\":0,\"error_msg\":\"success\",\"delta\":{\"content\":\"OK\"}}\n\ndata: {\"error_code\":\"0\",\"text\":\"[DONE]\"}\n\n", "OK", ""},
		{"reasoning frame", "event: reasoning\ndata: {\"text\":\"private reasoning\"}\n\ndata: {\"type\":\"analysis\",\"text\":\"private reasoning\"}\n\ndata: {\"delta\":{\"content\":\"OK\"}}\n\ndata: {\"text\":\"[DONE]\"}\n\n", "OK", ""},
		{"empty tools", "data: {\"delta\":{\"content\":\"OK\",\"tool_calls\":[]},\"function_call\":{}}\n\ndata: {\"text\":\"[DONE]\"}\n\n", "OK", ""},
		{"truncated", "data: {\"delta\":{\"content\":\"partial\"}}\n\n", "partial", "incomplete_stream"},
		{"unnamed terminal event", "event: done\ndata: {}\n\n", "", "incomplete_stream"},
		{"bare OpenAI done", "data: [DONE]\n\n", "", "invalid_upstream_response"},
		{"malformed JSON", "data: {\n\n", "", "invalid_upstream_response"},
		{"invalid delta", "data: {\"delta\":{\"content\":[]}}\n\n", "", "invalid_upstream_response"},
		{"null event", "data: null\n\n", "", "invalid_upstream_response"},
		{"named error", "event: error\ndata: {\"error_msg\":\"test-security-token Signature=private\"}\n\n", "", "upstream_error"},
		{"empty named error", "event: error\n\n", "", "upstream_error"},
		{"numeric error", "data: {\"error_code\":123,\"error_msg\":\"test-sk\"}\n\n", "", "upstream_error"},
		{"string error", "data: {\"error_code\":\"SVC.private\"}\n\n", "", "upstream_error"},
		{"expired credentials", "data: {\"error_code\":401}\n\n", "", "madao_login_required"},
		{"rate limit", "data: {\"error_code\":\"429\"}\n\n", "", "quota_exceeded"},
		{"invalid error code", "data: {\"error_code\":{}}\n\n", "", "invalid_upstream_response"},
		{"tool delta", "data: {\"delta\":{\"content\":\"hidden\",\"tool_calls\":[{\"function\":{\"name\":\"bash\"}}]}}\n\n", "", "ask_only_violation"},
		{"tool output", "data: {\"output\":[{\"type\":\"function_call\",\"name\":\"bash\"}]}\n\n", "", "ask_only_violation"},
		{"legacy function", "data: {\"delta\":{\"function_call\":{\"name\":\"bash\"}}}\n\n", "", "ask_only_violation"},
		{"top level tools", "data: {\"tool_calls\":[{\"id\":\"tool\"}]}\n\n", "", "ask_only_violation"},
		{"tool permission", "event: tool_authorization\n\n", "", "ask_only_violation"},
		{"camel tool event", "data: {\"type\":\"toolExecution\"}\n\n", "", "ask_only_violation"},
		{"execution", "data: {\"type\":\"execution_request\"}\n\n", "", "ask_only_violation"},
		{"permission", "data: {\"type\":\"permission_request\"}\n\n", "", "ask_only_violation"},
		{"tool before done", "data: {\"text\":\"[DONE]\",\"output\":[{\"type\":\"function_call\"}]}\n\n", "", "ask_only_violation"},
		{"rewritten answer", "data: {\"text\":\"partial\"}\n\ndata: {\"text\":\"replacement\"}\n\n", "partial", "invalid_upstream_response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var chunks []string
			result, err := readAskEventStream(context.Background(), strings.NewReader(test.stream), func(chunk string) error {
				chunks = append(chunks, chunk)
				return nil
			})
			if result.Text != test.text || strings.Join(chunks, "") != test.text {
				t.Fatalf("text=%q, chunks=%q, expected=%q", result.Text, chunks, test.text)
			}
			if test.code != "" {
				assertAskError(t, err, test.code)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

type askFailingReader struct{}

func (askFailingReader) Read([]byte) (int, error) {
	return 0, errors.New("transport echoed test-security-token Signature=private")
}

func TestAskEventStreamReaderAndCallbackErrors(t *testing.T) {
	body := io.MultiReader(strings.NewReader("data: {\"delta\":{\"content\":\"partial\"}}\n\n"), askFailingReader{})
	result, err := readAskEventStream(context.Background(), body, nil)
	if result.Text != "partial" {
		t.Fatalf("partial reply was lost: %q", result.Text)
	}
	assertAskError(t, err, "incomplete_stream")
	callbackErr := errors.New("client stopped reading")
	result, err = readAskEventStream(context.Background(), strings.NewReader(askTestStream), func(string) error { return callbackErr })
	if err != callbackErr || result.Text != "你好" {
		t.Fatalf("callback error was changed: text=%q, error=%v", result.Text, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err = readAskEventStream(ctx, strings.NewReader(askTestStream), func(string) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) || result.Text != "你好" {
		t.Fatalf("cancellation was changed: text=%q, error=%v", result.Text, err)
	}
}

func TestAskEventStreamFrameLimit(t *testing.T) {
	body := strings.Repeat(":heartbeat "+strings.Repeat("x", 1024)+"\n", askMaxFrameBytes/1024+1)
	_, err := readAskEventStream(context.Background(), strings.NewReader(body), nil)
	assertAskError(t, err, "invalid_upstream_response")
	_, err = readAskEventStream(context.Background(), strings.NewReader("data: "+strings.Repeat("x", askMaxFrameBytes)), nil)
	assertAskError(t, err, "invalid_upstream_response")
}

func TestAskVerifySession(t *testing.T) {
	tests := []struct {
		name, body, code string
		status           int
	}{
		{"native user", `{"user_id":"user-id","user_name":"user-name"}`, "", 200},
		{"wrapped user", `{"data":{"data":{"data":{"user_id":"user-id","user_name":"user-name"}}}}`, "", 200},
		{"missing name", `{"user_id":"user-id"}`, "madao_login_required", 200},
		{"missing id", `{"user_name":"user-name"}`, "madao_login_required", 200},
		{"invalid JSON", `{`, "invalid_upstream_response", 200},
		{"expired", `{"error_msg":"test-security-token Signature=private"}`, "madao_login_required", 401},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/snap-manager/v1/current/user" {
					t.Error("incorrect native current-user route")
				}
				verifyAskTestSignature(t, r, nil)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer upstream.Close()
			me, err := askTestAdapter(upstream.URL).verifyAskSession(context.Background(), askTestCredential())
			if test.code != "" {
				assertAskError(t, err, test.code)
			} else if err != nil || me.UserID != "user-id" || me.UserName != "user-name" || me.NickName != "user-name" {
				t.Fatalf("invalid native user translation: %#v, %v", me, err)
			}
		})
	}
}

func TestAskModelCatalogs(t *testing.T) {
	tests := []struct {
		name, builtin, agent string
		models               []builtinModel
		calls                int32
	}{
		{"builtin", `{"builtinModels":[{"model_id":"GLM-5.2","model_name":"GLM label","description":"description"},{"id":"GLM-5.2","label":"duplicate"},{"id":"hidden","display_enabled":false},{"id":"qwen","label":"Qwen label"}]}`, "", []builtinModel{{ID: "GLM-5.2", Label: "GLM label", Description: "description"}, {ID: "qwen", Label: "Qwen label"}}, 1},
		{"wrapped builtin", `{"data":{"data":{"data":{"builtinModels":[{"model_name":"Label","model_parameters":{"model_id":"GLM-5.2","model_desc":"description"}}]}}}}`, "", []builtinModel{{ID: "GLM-5.2", Label: "Label", Description: "description"}}, 1},
		{"agent fallback", `{"builtinModels":[]}`, `{"gpts":{"models":[{"model_name":"Label","model_alias":"alias","model_parameters":{"model_id":"GLM-5.2","model_desc":"description","display_enabled":true}},{"model_name":"Hidden","model_parameters":{"model_id":"hidden","display_enabled":false}}]}}`, []builtinModel{{ID: "GLM-5.2", Label: "Label", Description: "description"}}, 2},
		{"invalid builtin fallback", `{"builtinModels":{}}`, `{"data":{"gpts":{"models":[{"model_name":"Label","model_parameters":{"model_id":"GLM-5.2"}}]}}}`, []builtinModel{{ID: "GLM-5.2", Label: "Label"}}, 2},
		{"built-in defaults", `{}`, `{}`, kernelModels, 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				verifyAskTestSignature(t, r, nil)
				switch r.URL.Path {
				case "/v1/model/builtin":
					if r.Header.Get("Agent-Type") != "PromptCenter" {
						t.Error("incorrect native builtin-model routing")
					}
					_, _ = io.WriteString(w, test.builtin)
				case "/v1/agent-center/agents/detail":
					if r.Header.Get("Agent-Type") != "AgentCenter" || r.Header.Get("area") != "green" || r.URL.Query().Get("agent_id") != "9f0e5cb64a104dcfb4aa90dbedab7dc9" {
						t.Error("incorrect native Ask agent catalog")
					}
					_, _ = io.WriteString(w, test.agent)
				default:
					t.Error("unexpected model route")
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			models := askTestAdapter(upstream.URL).listAskModels(context.Background(), askTestCredential())
			if !reflect.DeepEqual(models, test.models) || calls.Load() != test.calls {
				t.Fatalf("models=%#v, calls=%d", models, calls.Load())
			}
		})
	}
}

func TestAskCompletionAuthenticationAndHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusFound, http.StatusOK} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Location", "/private-redirect")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error_msg":"test-ak test-sk test-security-token Signature=private"}`)
			}))
			defer upstream.Close()
			_, err := askTestAdapter(upstream.URL).askCompletion(context.Background(), askTestCredential(), "hello", "GLM-5.2", nil)
			code := "invalid_upstream_response"
			switch status {
			case 401, 403:
				code = "madao_login_required"
			case 429:
				code = "quota_exceeded"
			case 502:
				code = "upstream_error"
			}
			assertAskError(t, err, code)
			if calls.Load() != 1 {
				t.Fatal("signed requests must not follow redirects")
			}
		})
	}
	for _, c := range []credential{{}, {Nickname: "fallback-name", IAM: &iamGrant{}}, {IAM: &iamGrant{Credentials: askTestCredential().IAM.Credentials}}} {
		_, err := askTestAdapter("http://127.0.0.1:1").askCompletion(context.Background(), c, "hello", "GLM-5.2", nil)
		assertAskError(t, err, "madao_login_required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := askTestAdapter("http://127.0.0.1:1").askCompletion(ctx, askTestCredential(), "hello", "GLM-5.2", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was changed: %v", err)
	}
}

type askTransportFunc func(*http.Request) (*http.Response, error)

func (transport askTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return transport(r)
}

func TestAskTransportErrorsAreRedacted(t *testing.T) {
	a := askTestAdapter("http://codearts.test")
	failure := askTransportFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport echoed test-security-token Signature=private")
	})
	a.streamClient.Transport = failure
	a.client.Transport = failure
	_, err := a.askCompletion(context.Background(), askTestCredential(), "hello", "GLM-5.2", nil)
	assertAskError(t, err, "upstream_unavailable")
	_, err = a.verifyAskSession(context.Background(), askTestCredential())
	assertAskError(t, err, "upstream_unavailable")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.streamClient.Transport = askTransportFunc(func(*http.Request) (*http.Response, error) {
		cancel()
		return nil, errors.New("transport echoed test-security-token Signature=private")
	})
	_, err = a.askCompletion(ctx, askTestCredential(), "hello", "GLM-5.2", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("transport cancellation was changed: %v", err)
	}
}
