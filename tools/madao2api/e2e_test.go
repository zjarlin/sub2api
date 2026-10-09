package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// 通过已授权的真实适配器验证完整对话路径；普通测试不访问上游账号。
func TestAskConversationE2E(t *testing.T) {
	endpoint := strings.TrimRight(os.Getenv("MADAO_E2E_URL"), "/")
	key := os.Getenv("MADAO_E2E_KEY")
	if endpoint == "" || key == "" {
		t.Skip("set MADAO_E2E_URL and MADAO_E2E_KEY for real Ask acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 90 * time.Second}
	call := func(method, path string, body any) (int, http.Header, []byte) {
		t.Helper()
		var data []byte
		if body != nil {
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, response.Header, payload
	}
	t.Run("native models", func(t *testing.T) {
		status, _, body := call(http.MethodGet, "/v1/models", nil)
		if status != 200 || !bytes.Contains(body, []byte(`"id":"GLM-5.2"`)) {
			t.Fatalf("model catalog HTTP %d", status)
		}
	})
	t.Run("completion", func(t *testing.T) {
		status, _, body := call(http.MethodPost, "/v1/chat/completions", map[string]any{
			"model": "GLM-5.2", "messages": []map[string]string{{"role": "user", "content": "只回复 ASK_E2E_OK。"}},
		})
		var completion struct {
			Choices []struct {
				Message struct {
					Content   string
					ToolCalls json.RawMessage `json:"tool_calls"`
				}
				FinishReason string `json:"finish_reason"`
			}
		}
		if status != 200 || json.Unmarshal(body, &completion) != nil || len(completion.Choices) != 1 {
			t.Fatalf("completion HTTP %d", status)
		}
		choice := completion.Choices[0]
		if !strings.Contains(choice.Message.Content, "ASK_E2E_OK") || choice.FinishReason != "stop" || len(choice.Message.ToolCalls) != 0 {
			t.Fatal("completion did not return a finished text answer")
		}
		t.Logf("reply=%q", choice.Message.Content)
	})
	t.Run("streaming conversation history", func(t *testing.T) {
		status, headers, body := call(http.MethodPost, "/v1/chat/completions", map[string]any{
			"model": "GLM-5.2", "stream": true, "messages": []map[string]string{
				{"role": "user", "content": "请记住本次对话的暗号为 ASK_MULTI_884。"},
				{"role": "assistant", "content": "已记住暗号。"},
				{"role": "user", "content": "仅回复我之前给出的暗号。"},
			},
		})
		if status != 200 || !strings.HasPrefix(headers.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("stream HTTP %d", status)
		}
		var text strings.Builder
		done, finished := false, false
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "[DONE]" {
				done = true
				continue
			}
			var event struct {
				Error   json.RawMessage
				Choices []struct {
					Delta struct {
						Content   string
						ToolCalls json.RawMessage `json:"tool_calls"`
					}
					FinishReason *string `json:"finish_reason"`
				}
			}
			if json.Unmarshal([]byte(payload), &event) != nil || len(event.Error) > 0 {
				t.Fatal("stream returned an error event")
			}
			for _, choice := range event.Choices {
				if len(choice.Delta.ToolCalls) > 0 {
					t.Fatal("stream returned a tool call")
				}
				text.WriteString(choice.Delta.Content)
				if choice.FinishReason != nil && *choice.FinishReason == "stop" {
					finished = true
				}
			}
		}
		if !done || !finished || !strings.Contains(text.String(), "ASK_MULTI_884") {
			t.Fatal("stream did not retain conversation history or finish")
		}
		t.Logf("stream reply=%q", text.String())
	})
	t.Run("conversation has no shell execution", func(t *testing.T) {
		status, _, body := call(http.MethodPost, "/v1/chat/completions", map[string]any{
			"model": "GLM-5.2", "messages": []map[string]string{{"role": "user", "content": "请通过执行 bash 的 pwd 来回答当前工作目录。如果你不能执行，就仅回复 NO_TOOL。"}},
		})
		var completion struct {
			Choices []struct{ Message struct{ Content string } }
		}
		if status != 200 || json.Unmarshal(body, &completion) != nil || len(completion.Choices) != 1 || !strings.Contains(completion.Choices[0].Message.Content, "NO_TOOL") {
			t.Fatalf("Ask did not answer as text-only conversation; HTTP %d", status)
		}
		t.Logf("reply=%q", completion.Choices[0].Message.Content)
	})
	t.Run("client tools rejected", func(t *testing.T) {
		status, _, _ := call(http.MethodPost, "/v1/chat/completions", map[string]any{
			"model": "GLM-5.2", "messages": []map[string]string{{"role": "user", "content": "hi"}},
			"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "shell", "parameters": map[string]string{"type": "object"}}}},
		})
		if status != 400 {
			t.Fatalf("client tools HTTP %d", status)
		}
	})
}
