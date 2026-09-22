package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"glm-zcode-2api/internal/config"
)

// 服务端显式凭据无需依赖桌面文件，认证、工具调用和模型 ID 经完整 HTTP 转换链验证。
func TestSidecarExplicitCredential(t *testing.T) {
	upstream, capture := fakeUpstream(t, sseScript)
	cfg := config.Default()
	cfg.APIKey = "local-key"
	cfg.Upstream.APIKey = "sidecar-upstream-key"
	cfg.Upstream.BaseURL = upstream.URL
	cfg.Upstream.CredentialConfigPath = filepath.Join(t.TempDir(), "missing.json")
	handler := New(cfg, log.New(io.Discard, "", 0)).Handler()
	response := post(t, handler, chatBody(false))
	if response.Code != http.StatusOK {
		t.Fatalf("chat status = %d: %s", response.Code, response.Body.String())
	}
	_, headers := capture.last(t)
	if headers.Get("x-api-key") != "sidecar-upstream-key" {
		t.Fatal("explicit upstream credential was not used")
	}
	var payload struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				ToolCalls []json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Model != "glm-5.3" || len(payload.Choices) != 1 || len(payload.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("model ID or tool calls lost: %s", response.Body.String())
	}
}

// 缺少上游凭据时编排可启动，但就绪检查和真实请求必须返回明确错误。
func TestSidecarWithoutCredential(t *testing.T) {
	cfg := config.Default()
	cfg.APIKey = "local-key"
	cfg.Upstream.CredentialConfigPath = filepath.Join(t.TempDir(), "missing.json")
	handler := New(cfg, log.New(io.Discard, "", 0)).Handler()
	for path, status := range map[string]int{"/livez": http.StatusOK, "/healthz": http.StatusServiceUnavailable} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != status {
			t.Fatalf("%s status = %d, want %d", path, response.Code, status)
		}
	}
	if response := post(t, handler, chatBody(false)); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("chat status = %d, want 503", response.Code)
	}
}
