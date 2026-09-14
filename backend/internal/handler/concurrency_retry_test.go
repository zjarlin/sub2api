//go:build unit

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const capacityBody = `{"error":{"code":"TOO_MANY_REQUESTS","type":"billing_error","message":"In-flight request cap reached (1). Retry shortly."}}`

type capacityUpstream struct {
	service.HTTPUpstream
	ids []int64
}

func (u *capacityUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.ids = append(u.ids, id)
	if len(u.ids) == 1 {
		return &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(capacityBody))}, nil
	}
	return (&fallbackTestUpstream{}).Do(req, "", id, 1)
}

func TestUpstreamConcurrencyHTTPRecovery(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat/completions"} {
		for _, count := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%d_accounts", endpoint, count), func(t *testing.T) {
				repo := &grokCredentialHandlerRepo{}
				for id := 1; id <= count; id++ {
					repo.accounts = append(repo.accounts, service.Account{ID: int64(id), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
						Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: id,
						Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}},
						Extra:       map[string]any{"openai_passthrough": true}})
				}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Gateway.MaxAccountSwitches = 10
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				defer billing.Stop()
				upstream := &capacityUpstream{}
				gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				cache := &concurrencyCacheMock{acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }, acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }}
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
				key := &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive}}
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
				})
				router.POST("/responses", h.Responses)
				router.POST("/chat/completions", h.ChatCompletions)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest("POST", "/"+endpoint, strings.NewReader(`{"model":"deepseek-v4.1-flash","input":"hi","messages":[{"role":"user","content":"hi"}],"stream":false}`)))
				require.Equal(t, 200, response.Code, response.Body.String())
				require.Equal(t, []int64{1, int64(count)}, upstream.ids)
				require.Empty(t, repo.setErrorIDs)
				require.Empty(t, repo.setTempIDs)
			})
		}
	}
}

func TestUpstreamConcurrencyRetryBoundaries(t *testing.T) {
	r := concurrencyRetry{}
	err := &service.UpstreamFailoverError{StatusCode: 429, ResponseBody: []byte(capacityBody)}
	r.record(1, err)
	excluded := map[int64]struct{}{1: {}, 2: {}}
	for i := 0; i < 3; i++ {
		require.True(t, r.retry(context.Background(), excluded))
		require.NotContains(t, excluded, int64(1))
		require.Contains(t, excluded, int64(2))
		excluded[1] = struct{}{}
	}
	require.False(t, r.retry(context.Background(), excluded))
	r = concurrencyRetry{}
	r.record(1, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, r.retry(ctx, excluded))
	require.Contains(t, excluded, int64(1))
	r.record(1, &service.UpstreamFailoverError{StatusCode: 401})
	require.False(t, r.retry(context.Background(), excluded), "later credential failures must not be reopened")
}
