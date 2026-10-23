package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func streamingAdapter(t *testing.T, completion http.HandlerFunc, deleted chan struct{}) *adapter {
	t.Helper()
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/chat_session/create":
			_ = json.NewEncoder(w).Encode(testEnvelope(map[string]any{"chat_session": map[string]string{"id": "session"}}))
		case "/chat/create_pow_challenge":
			_ = json.NewEncoder(w).Encode(testEnvelope(map[string]any{"challenge": map[string]any{"target_path": completionPath}}))
		case "/chat/completion":
			w.Header().Set("Content-Type", "text/event-stream")
			completion(w, r)
		case "/chat_session/delete":
			_ = json.NewEncoder(w).Encode(testEnvelope(nil))
			if deleted != nil {
				once.Do(func() { close(deleted) })
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	a, err := newAdapter("test-key", filepath.Join(t.TempDir(), "accounts.json"), &upstreamClient{http: upstream.Client(), baseURL: upstream.URL}, refreshTestPow{})
	if err != nil {
		t.Fatal(err)
	}
	a.accounts = []webCredential{{UID: "test-user", Token: "test-token", DeviceID: "test-device"}}
	return a
}

func sendPatch(w http.ResponseWriter, path, operation string, value any) {
	data, _ := json.Marshal(map[string]any{"p": path, "o": operation, "v": value})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	_ = http.NewResponseController(w).Flush()
}

func TestChatStreamsBeforeUpstreamCompletes(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	a := streamingAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		sendPatch(w, "response/content", "APPEND", "first")
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		sendPatch(w, "response/content", "APPEND", " second")
		sendPatch(w, "response/status", "SET", "FINISHED")
	}, nil)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"deepseek-web-chat","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`))
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	var head strings.Builder
	for !strings.Contains(head.String(), `"content":"first"`) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("first token was buffered until upstream completion: %v", err)
		}
		head.WriteString(line)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" || strings.Contains(head.String(), "[DONE]") {
		t.Fatal("invalid early streaming response")
	}
	once.Do(func() { close(release) })
	tail, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(tail), `"content":" second"`) || !strings.Contains(string(tail), `"choices":[],`) || !strings.Contains(string(tail), "[DONE]") {
		t.Fatalf("invalid stream tail: %v %s", err, tail)
	}
}

func TestChatStreamErrorDoesNotReportSuccess(t *testing.T) {
	for _, suffix := range []string{"", "event: toast\ndata: {\"type\":\"error\",\"content\":\"do not leak\"}\n\n", "data: {\"code\":40003}\n\n"} {
		t.Run(suffix, func(t *testing.T) {
			var attempts atomic.Int32
			a := streamingAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				sendPatch(w, "response/content", "APPEND", "partial")
				_, _ = io.WriteString(w, suffix)
			}, nil)
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"deepseek-web-chat","messages":[{"role":"user","content":"hi"}],"stream":true}`))
			a.chat(w, r)
			body := w.Body.String()
			if attempts.Load() != 1 || !strings.Contains(body, `"error"`) || strings.Contains(body, "[DONE]") || strings.Contains(body, `"finish_reason":"stop"`) || strings.Contains(body, "do not leak") {
				t.Fatalf("partial error retried, leaked, or completed: %s", body)
			}
		})
	}
}

func TestChatCancelClosesUpstreamAndDeletesSession(t *testing.T) {
	canceled, deleted := make(chan struct{}), make(chan struct{})
	a := streamingAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		sendPatch(w, "response/content", "APPEND", "first")
		<-r.Context().Done()
		close(canceled)
	}, deleted)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"deepseek-web-chat","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()
	for name, done := range map[string]<-chan struct{}{"upstream": canceled, "cleanup": deleted} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s was not canceled/cleaned up", name)
		}
	}
}

func TestChatToolsStreamAndNonStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			a := streamingAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				var input map[string]any
				if json.NewDecoder(r.Body).Decode(&input) != nil || input["thinking_enabled"] != true || input["search_enabled"] != true || input["stream"] != true {
					t.Errorf("flags not forwarded: %v", input)
				}
				if r.Header.Get("X-Client-Platform") != "web" || r.Header.Get("Origin") == "" || r.Header.Get("Sec-Fetch-Mode") != "cors" {
					t.Error("missing browser headers")
				}
				sendPatch(w, "response/thinking_content", "APPEND", "considering")
				for _, part := range []string{"preface ", "<｜D", lookupCall[len("<｜D"):]} {
					sendPatch(w, "response/content", "APPEND", part)
				}
				sendPatch(w, "response/status", "SET", "FINISHED")
			}, nil)
			body := fmt.Sprintf(`{"model":"deepseek-web-reasoner","messages":[{"role":"user","content":"hi"}],"stream":%t,"tools":%s,"tool_choice":"required","search_enabled":true}`, stream, lookupTools)
			w := httptest.NewRecorder()
			a.chat(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
			output := w.Body.String()
			if w.Code != 200 || !strings.Contains(output, `"finish_reason":"tool_calls"`) || !strings.Contains(output, `"name":"lookup"`) || !strings.Contains(output, `"reasoning_content":"considering"`) || strings.Contains(output, "DSML") {
				t.Fatalf("tool conversion failed: %d %s", w.Code, output)
			}
			if stream && (!strings.Contains(output, "[DONE]") || strings.Count(output, `"content":"preface "`) != 1) {
				t.Fatalf("stream tool prefix duplicated or missing: %s", output)
			}
		})
	}
}

func TestChatRetriesRejectedProofOnlyOnce(t *testing.T) {
	for _, status := range []int{200, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var attempts atomic.Int32
			a := streamingAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"code":0,"data":{"biz_code":40301}}`)
					return
				}
				sendPatch(w, "response/content", "APPEND", "ok")
				sendPatch(w, "response/status", "SET", "FINISHED")
			}, nil)
			result, err := a.completeChat(context.Background(), a.accounts[0], "hi", chatOptions{})
			if err != nil || result.content != "ok" || attempts.Load() != 2 {
				t.Fatalf("proof retry failed: %+v %v attempts=%d", result, err, attempts.Load())
			}
		})
	}
}

func TestChatInvalidToolNeverReportsCompletion(t *testing.T) {
	for _, stream := range []bool{false, true} {
		a := streamingAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			sendPatch(w, "response/content", "APPEND", strings.TrimSuffix(lookupCall, "</｜DSML｜ calls>"))
			sendPatch(w, "response/status", "SET", "FINISHED")
		}, nil)
		w := httptest.NewRecorder()
		body := fmt.Sprintf(`{"model":"deepseek-web-chat","messages":[{"role":"user","content":"hi"}],"stream":%t,"tools":%s}`, stream, lookupTools)
		a.chat(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
		if w.Code != 502 || !strings.Contains(w.Body.String(), "invalid_tool_response") || strings.Contains(w.Body.String(), "DSML") || strings.Contains(w.Body.String(), "[DONE]") {
			t.Fatalf("invalid tool stream=%v: %d %s", stream, w.Code, w.Body.String())
		}
	}
}
