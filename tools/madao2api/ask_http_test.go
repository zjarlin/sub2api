package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAskChatHTTPStreamingAndCompletion(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "completion", true: "stream"}[streaming], func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != askChatPath {
					t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, askTestStream)
			}))
			defer upstream.Close()
			a, err := newAdapter("test-key", t.TempDir()+"/state.json", upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			a.credential = askTestCredential()
			a.credential.IAM.Credentials.Expiration = time.Now().Add(time.Hour).Format(time.RFC3339)
			body, err := json.Marshal(map[string]any{"model": "GLM-5.2", "messages": []map[string]string{{"role": "user", "content": "hello"}}, "stream": streaming})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
			req.Header.Set("Authorization", "Bearer test-key")
			rec := httptest.NewRecorder()
			a.handler().ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
			}
			if streaming {
				if !strings.Contains(rec.Body.String(), `"finish_reason":"stop"`) || !strings.HasSuffix(rec.Body.String(), "data: [DONE]\n\n") {
					t.Fatalf("invalid stream: %s", rec.Body)
				}
			} else {
				var response struct {
					Choices []struct{ Message struct{ Content string } }
				}
				if json.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.Choices) != 1 || response.Choices[0].Message.Content != "你好 世界" {
					t.Fatalf("invalid completion: %s", rec.Body)
				}
			}
			if calls != 1 {
				t.Fatalf("upstream calls=%d", calls)
			}
		})
	}
}

func TestAskHTTPFailureDoesNotReportSuccessfulCompletion(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "before content", true: "after content"}[partial], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if partial {
					_, _ = io.WriteString(w, "data: {\"delta\":{\"content\":\"partial\"}}\n\n")
				}
				_, _ = io.WriteString(w, "data: {\"type\":\"tool_call\"}\n\n")
			}))
			defer upstream.Close()
			a, err := newAdapter("test-key", t.TempDir()+"/state.json", upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			a.streamChat(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), askTestCredential(), "hi", "GLM-5.2", 0)
			if !partial && rec.Code != 502 {
				t.Fatalf("HTTP %d", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), `"ask_only_violation"`) || strings.Contains(rec.Body.String(), `"finish_reason":"stop"`) {
				t.Fatalf("error reported as success: %s", rec.Body)
			}
		})
	}
}
