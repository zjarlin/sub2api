package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 使用真实网页响应的字段结构，保留状态/推理/工具事件，验证只输出正文。
const cloudAgentStream = "event: status\ndata: {\"status\":\"processing\"}\n\n" +
	"event: thought\ndata: {\"content\":\"internal thought\"}\n\n" +
	"event: tool_result\ndata: {\"result\":\"internal result\"}\n\n" +
	"event: message\ndata: {\"content\":\"subagent text\",\"subagent_id\":\"child\"}\n\n" +
	"event: message\ndata: {\"content\":\"O\",\"part_id\":\"part1\"}\n\n" +
	"event: message\ndata: {\"content\":\"K\",\"part_id\":\"part1\"}\n\n" +
	"event: step_finish\ndata: {\"reason\":\"stop\",\"tokens\":{\"total\":30813}}\n\n" +
	"event: done\ndata: {\"model\":\"GLM-5.2\",\"tokens\":{\"input\":6362,\"output\":2}}\n\n"

func TestCloudAgentChatProtocol(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "completion", true: "stream"}[streaming], func(t *testing.T) {
			var calls []string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				if r.Header.Get("cftk") != "csrf" || r.Header.Get(scenarioHeaderKey) != "web" || r.Header.Get("Cookie") != "sso=credential" {
					t.Error("missing browser session authentication")
				}
				switch r.Method + " " + r.URL.Path {
				case "POST /chat/v1/cloudagent/sessions":
					if r.Header.Get(agentTypeHeaderKey) != "CodeBase" {
						t.Error("missing CodeBase routing header")
					}
					body, _ := io.ReadAll(r.Body)
					if string(body) != "{}" {
						t.Errorf("unexpected create body: %s", body)
					}
					_, _ = io.WriteString(w, `{"status":"ok","result":{"session_id":"temporary"}}`)
				case "POST /chat/codebaseservice/v1/cloudagent/sessions/temporary/messages":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["model_id"] != "GLM-5.2" || body["content"] != "[用户] Just say OK" || len(body) != 3 || !reflect.DeepEqual(body["repos"], []any{}) {
						t.Errorf("unexpected message body: %#v", body)
					}
					if r.Header.Get("Accept") != "text/event-stream" {
						t.Error("message POST must request SSE")
					}
					w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
					_, _ = io.WriteString(w, cloudAgentStream)
				case "DELETE /chat/v1/cloudagent/sessions/temporary":
					if r.Header.Get(agentTypeHeaderKey) != "CodeBase" || strings.Contains(r.Header.Get("Accept"), "event-stream") {
						t.Error("cleanup must use JSON and CodeBase routing")
					}
					_, _ = io.WriteString(w, `{"status":"ok"}`)
				default:
					t.Errorf("obsolete or unexpected route: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			a, err := newAdapter("test-key", t.TempDir()+"/credentials", upstream.URL+"/chat")
			if err != nil {
				t.Fatal(err)
			}
			a.credential = credential{UID: "test", Cftk: "csrf", Cookies: []cookiePair{{Name: "sso", Value: "credential"}}}
			body, _ := json.Marshal(map[string]any{"model": "GLM-5.2", "messages": []map[string]string{{"role": "user", "content": "Just say OK"}}, "stream": streaming})
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
			req.Header.Set("Authorization", "Bearer test-key")
			res := httptest.NewRecorder()
			a.handler().ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", res.Code, res.Body)
			}
			if streaming {
				if !strings.Contains(res.Body.String(), `"content":"O"`) || !strings.Contains(res.Body.String(), `"content":"K"`) || !strings.HasSuffix(res.Body.String(), "data: [DONE]\n\n") {
					t.Fatalf("invalid OpenAI stream: %s", res.Body)
				}
			} else {
				var response struct {
					Choices []struct {
						Message struct{ Content string }
					}
				}
				if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil || len(response.Choices) != 1 || response.Choices[0].Message.Content != "OK" {
					t.Fatalf("invalid completion: %s", res.Body)
				}
			}
			expected := []string{"POST /chat/v1/cloudagent/sessions", "POST /chat/codebaseservice/v1/cloudagent/sessions/temporary/messages", "DELETE /chat/v1/cloudagent/sessions/temporary"}
			if !reflect.DeepEqual(calls, expected) {
				t.Fatalf("unexpected lifecycle: %#v", calls)
			}
		})
	}
}

func TestCloudAgentModelsUseTopLevelCatalog(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/PromptCenterService/v1/agent-center/agents/detail" || r.Header.Get(agentTypeHeaderKey) != "AgentCenter" {
			t.Error("incorrect model catalog routing")
		}
		_, _ = io.WriteString(w, `{"gpts":{"models":[{"model_name":"Label","model_parameters":{"model_id":"GLM-5.2","display_enabled":true}},{"model_name":"Hidden","model_parameters":{"display_enabled":false}}]}}`)
	}))
	defer upstream.Close()
	a, err := newAdapter("test", t.TempDir()+"/state", upstream.URL+"/chat")
	if err != nil {
		t.Fatal(err)
	}
	models := a.listModels(context.Background(), credential{})
	if len(models) != 1 || models[0].ID != "GLM-5.2" || models[0].Label != "Label" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestCloudAgentSSEFramingAndFailures(t *testing.T) {
	tests := []struct {
		name   string
		stream string
		text   string
		code   string
	}{
		{"multiline CRLF", "event: message\r\ndata: {\r\ndata: \"content\":\"你好\"}\r\n\r\nevent: done\r\ndata: {}", "你好", ""},
		{"whitespace delta", "event: message\ndata: {\"content\":\"hello\"}\n\nevent: message\ndata: {\"content\":\" \"}\n\nevent: message\ndata: {\"content\":\"world\"}\n\nevent: done\ndata: {}\n\n", "hello world", ""},
		{"named error", "event: error\ndata: {\"message\":\"quota exhausted\"}\n\n", "", "upstream_error"},
		{"truncated", "event: message\ndata: {\"content\":\"partial\"}\n\n", "partial", "incomplete_stream"},
		{"broken JSON", "event: message\ndata: {\n\n", "", "invalid_upstream_response"},
		{"permission", "event: tool_authorization\ndata: {\"permission\":\"bash\"}\n\n", "", "interaction_required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := readEventStream(context.Background(), strings.NewReader(test.stream))
			if result.Text != test.text {
				t.Fatalf("text=%q, expected=%q", result.Text, test.text)
			}
			var upstream *upstreamError
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.As(err, &upstream) || upstream.code != test.code {
				t.Fatalf("expected %s, got %v", test.code, err)
			}
		})
	}
}

func TestCloudAgentFailureStillDeletesTemporarySession(t *testing.T) {
	var deleted bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deleted = true
		case http.MethodPost:
			if r.URL.Path == "/v1/cloudagent/sessions" {
				_, _ = io.WriteString(w, `{"result":{"session_id":"temporary"}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: error\ndata: {\"message\":\"failed\"}\n\n")
		}
	}))
	defer upstream.Close()
	a, err := newAdapter("test", t.TempDir()+"/state", upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	a.streamTimeout = time.Second
	_, err = a.generate(context.Background(), credential{}, "hi", "GLM-5.2", nil)
	if err == nil || !deleted {
		t.Fatalf("error=%v, deleted=%v", err, deleted)
	}
}

func TestCloudAgentStreamErrorDoesNotFinishSuccessfully(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "before content", true: "after content"}[partial], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					return
				}
				if r.URL.Path == "/v1/cloudagent/sessions" {
					_, _ = io.WriteString(w, `{"result":{"session_id":"temporary"}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if partial {
					_, _ = io.WriteString(w, "event: message\ndata: {\"content\":\"partial\"}\n\n")
				}
				_, _ = io.WriteString(w, "event: error\ndata: {\"message\":\"failed\"}\n\n")
			}))
			defer upstream.Close()
			a, err := newAdapter("test", t.TempDir()+"/state", upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			res := httptest.NewRecorder()
			a.streamChat(res, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), credential{}, "hi", "GLM-5.2", 0)
			if !partial && res.Code != http.StatusBadGateway {
				t.Fatalf("upstream failure returned HTTP %d", res.Code)
			}
			if !strings.Contains(res.Body.String(), `"error"`) || strings.Contains(res.Body.String(), `"finish_reason":"stop"`) {
				t.Fatalf("upstream failure reported as success: %s", res.Body)
			}
		})
	}
}

type interruptedStream struct{}

func (interruptedStream) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestCloudAgentReadErrorIsNotHidden(t *testing.T) {
	body := io.MultiReader(strings.NewReader("event: message\ndata: {\"content\":\"partial\"}\n\n"), interruptedStream{})
	result, err := readEventStream(context.Background(), body)
	if result.Text != "partial" || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("text=%q, error=%v", result.Text, err)
	}
}
