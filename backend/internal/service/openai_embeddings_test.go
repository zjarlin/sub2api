package service

import (
	"bytes"
	"context"
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

func TestBuildOpenAIEmbeddingsURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base string
		want string
	}{
		{"bare /v1", "http://127.0.0.1:11434/v1", "http://127.0.0.1:11434/v1/embeddings"},
		{"already embeddings", "https://api.example.com/v1/embeddings", "https://api.example.com/v1/embeddings"},
		{"api root", "https://generativelanguage.googleapis.com/v1beta/openai", "https://generativelanguage.googleapis.com/v1beta/openai/embeddings"},
		{"bare domain", "https://api.example.com", "https://api.example.com/v1/embeddings"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, buildOpenAIEmbeddingsURL(tt.base))
		})
	}
}

func TestForwardEmbeddings_OllamaAllowsEmptyAPIKeyAndPreservesResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"bge-m3","input":"hello"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"x-request-id": []string{"rid_embed"},
		},
		Body: io.NopCloser(strings.NewReader(`{"object":"list","data":[{"object":"embedding","embedding":[0.1,0.2],"index":0}],"model":"bge-m3","usage":{"prompt_tokens":3,"total_tokens":3}}`)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg: &config.Config{
			Security: config.SecurityConfig{
				URLAllowlist: config.URLAllowlistConfig{
					AllowInsecureHTTP: true,
				},
			},
		},
	}
	account := &Account{
		ID:          238,
		Name:        "zjarlin_ollama",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"vendor":   "ollama",
			"base_url": "http://host.docker.internal:11434/v1",
			"model_mapping": map[string]any{
				"bge-m3": "bge-m3",
			},
		},
	}

	result, err := svc.ForwardEmbeddings(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "bge-m3", result.Model)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "http://host.docker.internal:11434/v1/embeddings", upstream.lastReq.URL.String())
	require.Equal(t, "bge-m3", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, rec.Body.String(), `"embedding":[0.1,0.2]`)
}
