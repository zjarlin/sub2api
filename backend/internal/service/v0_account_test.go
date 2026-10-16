package service

import (
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestV0AccountValidation(t *testing.T) {
	for _, credentials := range []map[string]any{nil, {}, {"api_key": ""}, {"api_key": "secret", "base_url": "http://api.v0.dev/v2"}, {"api_key": "secret", "base_url": "https://internal.example/v2"}, {"api_key": "secret", "base_url": "https://api.v0.dev/v2?x=y"}} {
		if ValidateV0Account(PlatformV0, AccountTypeAPIKey, credentials) == nil {
			t.Fatalf("accepted invalid credentials")
		}
	}
	if err := ValidateV0Account(PlatformV0, AccountTypeOAuth, map[string]any{"api_key": "secret"}); err == nil {
		t.Fatal("accepted OAuth")
	}
	if err := ValidateV0Account(PlatformV0, AccountTypeAPIKey, map[string]any{"api_key": "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateV0Account(PlatformV0, AccountTypeAPIKey, map[string]any{"api_key": "secret", "base_url": V0DefaultBaseURL}); err != nil {
		t.Fatal(err)
	}
}

// v0 没有上游 /models 探测端点，模型目录必须由网关内置，否则后台模型列表为空。
func TestV0ModelCatalogMatchesDocumentedTiers(t *testing.T) {
	ids := V0DefaultModelIDs()
	require.Equal(t, []string{"v0-mini", "v0-pro", "v0-max", "v0-max-fast"}, ids)
	for _, model := range V0ModelCatalog() {
		require.NotEmpty(t, model.DisplayName)
	}
}

// 后台「测试」对 v0 必须走只读的会话列表端点，而不是聊天端点。
func TestV0AccountTestProbeUsesChatsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chats", r.URL.Path)
		require.Equal(t, "Bearer v0-secret", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chats":[{"id":"chat_1"}],"cursor":null}`))
	}))
	defer server.Close()

	account := &Account{
		Platform:    PlatformV0,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "v0-secret", "base_url": server.URL},
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/1/test", nil)

	service := &AccountTestService{}
	require.NoError(t, service.testV0Account(ctx.Request.Context(), ctx, account))
	require.Contains(t, recorder.Body.String(), "test_complete")
	require.Contains(t, recorder.Body.String(), "1 chats")
}

func TestV0AccountTestProbeReportsUpstreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key"}`))
	}))
	defer server.Close()

	account := &Account{
		Platform:    PlatformV0,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "bad", "base_url": server.URL},
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/1/test", nil)

	service := &AccountTestService{}
	err := service.testV0Account(ctx.Request.Context(), ctx, account)
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}
