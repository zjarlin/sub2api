//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAutoModelCostLimitExcludesHighestTier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"/v1/responses", "/v1/chat/completions"} {
		for _, tc := range []struct {
			name     string
			models   []string
			settings map[string]string
			status   int
			selected string
		}{
			{name: "one cheaper candidate", models: []string{"gpt-6-astra", "gpt-5.5"}, status: http.StatusOK, selected: "gpt-5.5"},
			{name: "highest tier only", models: []string{"gpt-6-astra"}, status: http.StatusServiceUnavailable},
			{name: "highest tier alias", models: []string{"gpt-6-astra-premium", "gpt-5.5"}, status: http.StatusOK, selected: "gpt-5.5", settings: map[string]string{
				service.SettingKeyModelAliases: `{"groups":[{"canonical":"gpt-6-astra","aliases":["gpt-6-astra-premium"]}]}`,
			}},
			{name: "configured highest tier with fallback disabled", models: []string{"gpt-6-astra", "gpt-5.5"}, status: http.StatusOK, selected: "gpt-6-astra", settings: map[string]string{
				service.SettingKeyModelFallbackPolicy: `{"enabled":false,"tiers":[{"name":"expensive","models":["gpt-5.5"]},{"name":"economy","models":["gpt-6-astra"]}]}`,
			}},
			{name: "invalid policy", models: []string{"gpt-6-astra", "gpt-5.5"}, status: http.StatusServiceUnavailable, settings: map[string]string{
				service.SettingKeyModelFallbackPolicy: `{"enabled":true,"tiers":[]}`,
			}},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				mapping := make(map[string]any, len(tc.models))
				for _, model := range tc.models {
					mapping[model] = model
				}
				accounts := []service.Account{autoModelTestAccounts()[0], autoModelTestAccounts()[2]}
				accounts[0].Credentials = map[string]any{"model_mapping": mapping}
				h := newAutoModelTestHandler(accounts)
				h.settingService = service.NewSettingService(&contentModerationHandlerSettingRepo{values: tc.settings}, nil)
				group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
				}, h.AutoModelMiddleware(nil))
				router.POST(endpoint, func(c *gin.Context) {
					body, err := io.ReadAll(c.Request.Body)
					require.NoError(t, err)
					c.String(http.StatusOK, gjson.GetBytes(body, "model").String())
				})
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{"model":"auto","input":"hi","messages":[{"role":"user","content":"hi"}]}`)))
				require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
				if tc.selected != "" {
					require.Equal(t, tc.selected, recorder.Body.String())
					require.Equal(t, tc.selected, recorder.Header().Get("X-Sub2API-Selected-Model"))
				}
				if tc.name == "highest tier only" {
					require.False(t, h.autoModelAvailable(context.Background(), group, tc.models))
					catalogRecorder := httptest.NewRecorder()
					catalogContext, _ := gin.CreateTestContext(catalogRecorder)
					catalogContext.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
					catalogContext.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
					h.Models(catalogContext)
					require.Equal(t, http.StatusOK, catalogRecorder.Code, catalogRecorder.Body.String())
					var catalog gatewayModelsResponseForTest
					require.NoError(t, json.Unmarshal(catalogRecorder.Body.Bytes(), &catalog))
					require.True(t, containsModelID(catalog.Data, "gpt-6-astra"))
					require.False(t, containsModelID(catalog.Data, autoModelID))
					manual := httptest.NewRecorder()
					router.ServeHTTP(manual, httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{"model":"gpt-6-astra","input":"hi"}`)))
					require.Equal(t, http.StatusOK, manual.Code, manual.Body.String())
					require.Equal(t, "gpt-6-astra", manual.Body.String())
				}
			})
		}
	}
}

func TestAutoModelSystemOneCannotSelectHighestTier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, chosen := range []string{"gpt-5.6-sol", "gpt-6-astra"} {
		t.Run(chosen, func(t *testing.T) {
			var criteria atomic.Value
			decision := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Questions map[string]struct {
						Criteria map[string]string `json:"criteria"`
					} `json:"questions"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				criteria.Store(request.Questions["model"].Criteria)
				_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"model": map[string]string{"choice": chosen}}})
			}))
			defer decision.Close()
			accounts := []service.Account{autoModelTestAccounts()[0], autoModelTestAccounts()[2]}
			accounts[0].Credentials["model_mapping"] = map[string]any{"gpt-6-astra": "gpt-6-astra", "gpt-5.6-sol": "gpt-5.6-sol", "gpt-5.5": "gpt-5.5"}
			accounts[1].Type = service.AccountTypeAPIKey
			accounts[1].Credentials["base_url"] = decision.URL
			h := newAutoModelTestHandler(accounts)
			group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
			}, h.AutoModelMiddleware(nil))
			router.POST("/v1/responses", func(c *gin.Context) {
				body, _ := io.ReadAll(c.Request.Body)
				c.String(http.StatusOK, gjson.GetBytes(body, "model").String())
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto","input":"hi"}`)))
			require.NotNil(t, criteria.Load())
			require.Equal(t, map[string]string{"gpt-5.6-sol": "openai text model gpt-5.6-sol", "gpt-5.5": "openai text model gpt-5.5"}, criteria.Load())
			if chosen == "gpt-6-astra" {
				require.Equal(t, http.StatusBadGateway, recorder.Code)
			} else {
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				require.Equal(t, chosen, recorder.Body.String())
			}
		})
	}
}

type autoCostCompositeRouteRepo struct {
	service.CompositeModelRouteRepository
	routes []service.CompositeModelRoute
}

func (r autoCostCompositeRouteRepo) ListByGroup(context.Context, int64, bool) ([]service.CompositeModelRoute, error) {
	return r.routes, nil
}

func TestAutoModelCompositeMappingCannotSelectHighestTier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := autoModelTestAccounts()
	h := newAutoModelTestHandler(accounts)
	group := &service.Group{ID: 71, Platform: service.PlatformComposite}
	resolver := service.NewCompositeRouteResolver(autoCostCompositeRouteRepo{routes: []service.CompositeModelRoute{{
		PublicModel: "gpt-5.5", UpstreamModel: "gpt-6-astra", TargetPlatform: service.PlatformOpenAI,
		MatchType: service.CompositeRouteMatchExact, Endpoint: service.CompositeRouteEndpointAny, Enabled: true,
	}}})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
	}, h.AutoModelMiddleware(resolver))
	router.POST("/v1/responses", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.String(http.StatusOK, gjson.GetBytes(body, "model").String())
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto","input":"hi"}`)))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "deepseek-v4-flash", recorder.Body.String())
}

func TestAutoModelChannelMappingCannotSelectHighestTier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := []service.Account{autoModelTestAccounts()[0], autoModelTestAccounts()[2]}
	repo := &autoModelAccountRepoStub{gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{71: accounts}}}
	channels := service.NewChannelService(&openAIWSUsageHandlerChannelRepoStub{
		channels: []service.Channel{{
			ID: 1, Status: service.StatusActive, GroupIDs: []int64{71},
			ModelMapping: map[string]map[string]string{service.PlatformOpenAI: {"gpt-5.5": "gpt-6-astra"}},
		}},
		groupPlatforms: map[int64]string{71: service.PlatformOpenAI},
	}, nil, nil, nil, nil)
	h := &GatewayHandler{gatewayService: service.NewGatewayService(repo,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, channels, nil, nil, nil, nil,
	)}
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
	}, h.AutoModelMiddleware(nil))
	router.POST("/v1/responses", func(c *gin.Context) {
		t.Error("excluded channel target reached the generation handler")
		c.Status(http.StatusOK)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto","input":"hi"}`)))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
	require.False(t, h.autoModelAvailable(context.Background(), group, []string{"gpt-5.5"}))
}

func TestAutoModelCostLimitSurvivesHTTPFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "chat/completions"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_%t", endpoint, stream), func(t *testing.T) {
				accounts := []service.Account{autoModelTestAccounts()[0], autoModelTestAccounts()[2]}
				accounts[0].Type = service.AccountTypeAPIKey
				accounts[0].Concurrency = 1
				accounts[0].Credentials = map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{
					"gpt-6-astra": "gpt-6-astra", "gpt-5.6-sol": "gpt-5.6-sol", "gpt-5.5": "gpt-5.5",
				}}
				accounts[0].Extra = map[string]any{"openai_passthrough": true}
				decision := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(`{"answers":{"model":{"choice":"gpt-5.6-sol"}}}`))
				}))
				defer decision.Close()
				accounts[1].Type = service.AccountTypeAPIKey
				accounts[1].Credentials["base_url"] = decision.URL
				cfg := &config.Config{RunMode: config.RunModeSimple}
				settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
					service.SettingKeyModelFallbackPolicy: `{"enabled":true,"tiers":[{"name":"highest","models":["gpt-6-astra"]},{"name":"standard","models":["gpt-5.6-sol","gpt-5.5"]}]}`,
				}}, cfg)
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				defer billing.Stop()
				upstream := &fallbackTestUpstream{status: http.StatusBadGateway}
				gateway := service.NewOpenAIGatewayService(&grokCredentialHandlerRepo{accounts: accounts}, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
					service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
				concurrency := service.NewConcurrencyService(&concurrencyCacheMock{
					acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
					acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
				})
				openAI := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
				h := newAutoModelTestHandler(accounts)
				h.settingService = settings
				group := &service.Group{ID: 71, Platform: service.PlatformOpenAI, Status: service.StatusActive}
				key := &service.APIKey{ID: 2, GroupID: &group.ID, Group: group, User: &service.User{ID: 3, Status: service.StatusActive}}
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
				}, h.AutoModelMiddleware(nil))
				router.POST("/v1/responses", openAI.Responses)
				router.POST("/v1/chat/completions", openAI.ChatCompletions)
				recorder := httptest.NewRecorder()
				body := fmt.Sprintf(`{"model":"auto","input":"hi","messages":[{"role":"user","content":"hi"}],"stream":%t}`, stream)
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body)))
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				require.Equal(t, []string{"gpt-5.6-sol", "gpt-5.5"}, upstream.models)
				require.Equal(t, "gpt-5.5", recorder.Header().Get("X-Sub2api-Fallback-Model"))
			})
		}
	}
}

func TestAutoModelFallbackRechecksCostExclusions(t *testing.T) {
	ctx, err := (*service.SettingService)(nil).BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	c.Set(modelFallbackStateKey, &modelFallbackState{candidates: []service.ModelFallbackCandidate{
		{Model: "gpt-6-astra", Tier: "highest"}, {Model: "gpt-5.5", Tier: "standard"},
	}})
	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
	attempt, ok := h.nextModelFallback(c, &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}, "gpt-5.6-sol", []byte(`{"input":"hi"}`), false)
	require.True(t, ok)
	require.Equal(t, "gpt-5.5", attempt.Model)
	_, ok = h.nextModelFallback(c, &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}, "gpt-5.5", []byte(`{"input":"hi"}`), false)
	require.False(t, ok)
}
