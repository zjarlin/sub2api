package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMediaTextCharacters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		contentType string
		body        string
		want        int
	}{
		{"text", "application/json", `{"text":"hello"}`, 5},
		{"input", "application/json; charset=utf-8", `{"input":"你好世界"}`, 4},
		{"prompt", "application/json", `{"prompt":"a cat"}`, 5},
		{"empty body", "application/json", ``, 0},
		{"non json", "application/octet-stream", `{"text":"hello"}`, 0},
		{"missing field", "application/json", `{"model":"manbo"}`, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mediaTextCharacters(tc.contentType, []byte(tc.body))
			if got != tc.want {
				t.Fatalf("mediaTextCharacters(%q, %q) = %d, want %d", tc.contentType, tc.body, got, tc.want)
			}
		})
	}
}

func TestMediaProxyDisabledReturnsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &GatewayHandler{}
	router.Any("/v1/media/*proxyPath", h.MediaProxy)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/media/tts", strings.NewReader(`{"text":"hi"}`))
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled media proxy status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestIsAllowedMediaEndpoint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		endpoint string
		method   string
		want     bool
	}{
		{"tts", "tts", http.MethodPost, true},
		{"openai speech", "v1/audio/speech", http.MethodPost, true},
		{"dub", "videos/dub", http.MethodPost, true},
		{"generations", "videos/generations", http.MethodPost, true},
		{"transcode", "videos/transcode", http.MethodPost, true},
		{"task status", "tasks/dub_abc", http.MethodGet, true},
		{"task content", "tasks/dub_abc/content", http.MethodGet, true},
		{"task status via POST rejected", "tasks/dub_abc", http.MethodPost, false},
		// 服务商中继：GET 搜索与 POST 生成都在白名单内。
		{"relay stock search", "relay/pexels/v1/videos/search", http.MethodGet, true},
		{"relay video create", "relay/ofox/videos", http.MethodPost, true},
		{"relay provider root", "relay/pixabay", http.MethodGet, true},
		{"unknown endpoint", "secrets", http.MethodPost, false},
		{"unknown relay-like", "relay", http.MethodGet, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isAllowedMediaEndpoint(tc.endpoint, tc.method); got != tc.want {
				t.Fatalf("isAllowedMediaEndpoint(%q, %q) = %v, want %v", tc.endpoint, tc.method, got, tc.want)
			}
		})
	}
}
