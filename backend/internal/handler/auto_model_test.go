package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/jev_api"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newAutoModelTestHandler(accounts []service.Account) *GatewayHandler {
	return newGatewayModelsHandlerForTest(&autoModelAccountRepoStub{gatewayModelsAccountRepoStub{
		byGroup: map[int64][]service.Account{71: accounts},
	}})
}

type autoModelAccountRepoStub struct {
	gatewayModelsAccountRepoStub
}

func (r *autoModelAccountRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]service.Account, error) {
	accounts, err := r.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	filtered := make([]service.Account, 0)
	for _, account := range accounts {
		if account.Platform == platform {
			filtered = append(filtered, account)
		}
	}
	return filtered, nil
}

func autoModelTestAccounts() []service.Account {
	return []service.Account{
		{ID: 1, Platform: service.PlatformOpenAI, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}}},
		{ID: 2, Platform: service.PlatformDeepseek, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"deepseek-v4-flash": "deepseek-v4-flash"}}},
		{ID: 3, Platform: service.PlatformJev, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{jev_api.ModelID: jev_api.ModelID}}},
	}
}

func TestAutoModelAppearsOnlyForRoutableCompositeGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name      string
		accounts  []service.Account
		allowlist service.GroupModelAllowlist
		wantAuto  bool
	}{
		{name: "decision account and text models", accounts: autoModelTestAccounts(), wantAuto: true},
		{name: "decision account missing", accounts: autoModelTestAccounts()[:2]},
		{name: "auto excluded by allowlist", accounts: autoModelTestAccounts(), allowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-5.5"}}},
		{name: "auto explicitly allowed", accounts: autoModelTestAccounts(), allowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"auto"}}, wantAuto: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAutoModelTestHandler(tc.accounts)
			group := &service.Group{ID: 71, Platform: service.PlatformComposite, ModelAllowlist: tc.allowlist}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
			h.Models(c)
			require.Equal(t, http.StatusOK, recorder.Code)
			var response gatewayModelsResponseForTest
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, tc.wantAuto, containsModelID(response.Data, autoModelID))
			if tc.wantAuto {
				require.Contains(t, h.codexModelIDsForGroup(context.Background(), group, ""), autoModelID)
			}
		})
	}
}

func TestAutoModelAppearsInCodexManifest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newAutoModelTestHandler(autoModelTestAccounts())
	group := &service.Group{ID: 71, Platform: service.PlatformComposite}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version=1.0", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
	h.CodexModels(c)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response codexModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	slugs := make([]string, 0, len(response.Models))
	for _, model := range response.Models {
		slugs = append(slugs, model.Slug)
	}
	require.Contains(t, slugs, autoModelID)
}

func TestAutoModelOpenAIGroupCatalogAndETag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newAutoModelTestHandler(autoModelTestAccounts())
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	request := func(path, etag string, write func(*gin.Context)) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil)
		c.Request.Header.Set("If-None-Match", etag)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
		write(c)
		return recorder
	}
	ordinary := request("/v1/models", "", h.Models)
	require.Equal(t, http.StatusOK, ordinary.Code, ordinary.Body.String())
	var ordinaryCatalog gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(ordinary.Body.Bytes(), &ordinaryCatalog))
	require.True(t, containsModelID(ordinaryCatalog.Data, autoModelID))

	manifestBody := []byte(`{"models":[{"slug":"gpt-5.5","visibility":"list"}]}`)
	manifest := func(c *gin.Context) {
		h.PrepareAutoModelListing(c)
		writeOpenAIModelsResponse(c, &service.OpenAIModelsResponse{Body: manifestBody})
	}
	first := request("/v1/models?client_version=1.0", "", manifest)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.Contains(t, first.Body.String(), `"slug":"auto"`)
	require.NotEmpty(t, first.Header().Get("ETag"))
	notModified := request("/v1/models?client_version=1.0", first.Header().Get("ETag"), manifest)
	require.Equal(t, http.StatusNotModified, notModified.Code)
	require.Empty(t, notModified.Body.String())
}

func TestAutoModelPinnedOpenAICatalogUsesDiscoveredModels(t *testing.T) {
	accounts := autoModelTestAccounts()
	accounts[0] = newPinnedCodexAccount(1, service.StatusActive, true, false)
	upstream := &codexModelsPinnedHTTPUpstream{bodies: map[int64]string{
		1: `{"data":[{"id":"gpt-5.5"},{"id":"gpt-image-1"}]}`,
	}}
	h := newAutoModelTestHandler(accounts)
	h.openAIGatewayService = newPinnedCodexTestHandler(accounts, upstream, 3).gatewayService
	h.maxAccountSwitches = 3
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI,
		CodexModelsManifestConfig: service.GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{1}}}
	require.Equal(t, []string{"gpt-5.5"}, autoModelCandidatesForGroup(group, h.autoModelCatalog(context.Background(), group)))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
	h.Models(c)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var catalog gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &catalog))
	require.True(t, containsModelID(catalog.Data, autoModelID))
}

func TestAutoModelOpenAIGroupNeedsTextAccount(t *testing.T) {
	accounts := []service.Account{autoModelTestAccounts()[2]}
	h := newAutoModelTestHandler(accounts)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	models := h.autoModelCatalog(context.Background(), group)
	require.NotEmpty(t, models)
	require.False(t, h.autoModelAvailable(context.Background(), group, models))
}

func TestAppendAutoModelRejectsInvalidCatalog(t *testing.T) {
	for _, body := range []string{`{"data":null}`, `{"models":{}}`, `{"error":"unavailable"}`} {
		_, err := appendAutoModelToCatalog([]byte(body))
		require.Error(t, err, body)
	}
}

func containsModelID(models []gatewayModelItemForTest, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func TestAutoModelCandidatesExcludeDecisionAndMediaModels(t *testing.T) {
	models := []string{
		"auto", "typesafe/jev", "laya", "gpt-image-1", "text-embedding-3-large", "gpt-4o-audio",
		"nvidia/riva-translate-4b-instruct-v2", "nvidia/riva-translate-4b-instruct-v1.1",
		"nvidia/llama-3.1-nemotron-safety-guard-8b-v3", "nvidia/nemotron-3.5-content-safety",
		"gpt-5.5", "deepseek-v4-flash",
	}
	require.Equal(t, []string{"gpt-5.5", "deepseek-v4-flash"}, autoModelCandidates(models))
	require.Equal(t, []string{"auto", "gpt-5.5"}, prependAutoModel([]string{"gpt-5.5", "auto"}))
}

func TestAutoModelMiddlewareFiltersToolIncompatibleCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	decisionCalls := 0
	decision := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decisionCalls++
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(body, "questions.model.criteria.gpt-tool-disabled").Exists())
		require.True(t, gjson.GetBytes(body, "questions.model.criteria.gpt-tool-ready-a").Exists())
		require.True(t, gjson.GetBytes(body, "questions.model.criteria.gpt-tool-ready-b").Exists())
		_, _ = w.Write([]byte(`{"answers":{"model":{"choice":"gpt-tool-ready-b"}}}`))
	}))
	defer decision.Close()

	accounts := autoModelTestAccounts()
	accounts[0].Credentials["model_mapping"] = map[string]any{
		"gpt-tool-disabled": "gpt-tool-disabled",
		"gpt-tool-ready-a":  "gpt-tool-ready-a",
		"gpt-tool-ready-b":  "gpt-tool-ready-b",
	}
	accounts[0].SetUpstreamModelMetadataSnapshot(service.UpstreamModelMetadataSnapshot{Models: map[string]service.UpstreamModelMetadata{
		"gpt-tool-disabled": {
			ID: "gpt-tool-disabled",
			CodexToolCapabilities: map[string]json.RawMessage{
				"supports_function_calling": json.RawMessage("false"),
			},
		},
		"gpt-tool-ready-a": {ID: "gpt-tool-ready-a"},
		"gpt-tool-ready-b": {ID: "gpt-tool-ready-b"},
	}})
	accounts[2].Type = service.AccountTypeAPIKey
	accounts[2].Credentials["base_url"] = decision.URL

	h := newAutoModelTestHandler(accounts)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
	}, h.AutoModelMiddleware(nil))
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.String(http.StatusOK, gjson.GetBytes(body, "model").String())
	})

	recorder := httptest.NewRecorder()
	request := `{"model":"auto","messages":[{"role":"user","content":"use a tool"}],"tools":[{"type":"function","function":{"name":"shell"}}]}`
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(request)))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "gpt-tool-ready-b", recorder.Body.String())
	require.Equal(t, 1, decisionCalls)
}

func TestAutoModelDecisionAcceptsOnlyCatalogCandidates(t *testing.T) {
	var selected atomic.Value
	selected.Store("deepseek-v4-flash")
	var status atomic.Int32
	status.Store(http.StatusOK)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/systemone", r.URL.Path)
		require.Equal(t, "Bearer decision-key", r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, jev_api.ModelID, gjson.GetBytes(body, "model").String())
		require.Equal(t, "choice", gjson.GetBytes(body, "questions.model.type").String())
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"answers":{"model":{"choice":"` + selected.Load().(string) + `"}}}`))
	}))
	defer upstream.Close()
	selection := &service.AccountSelectionResult{Account: &service.Account{
		Platform:    service.PlatformJev,
		Credentials: map[string]any{"api_key": "decision-key", "base_url": upstream.URL},
	}, Acquired: true}
	criteria := map[string]string{"gpt-5.5": "openai", "deepseek-v4-flash": "deepseek"}
	request := []byte(`{"model":"typesafe/jev","state":"task","questions":{"model":{"type":"choice","criteria":{"gpt-5.5":"openai","deepseek-v4-flash":"deepseek"}}}}`)
	model, err := relayAutoModelDecision(context.Background(), selection, request, jev_api.ModelID, criteria)
	require.NoError(t, err)
	require.Equal(t, selected.Load(), model)
	selected.Store("unavailable-model")
	_, err = relayAutoModelDecision(context.Background(), selection, request, jev_api.ModelID, criteria)
	require.Error(t, err)
	status.Store(http.StatusBadRequest)
	_, err = relayAutoModelDecision(context.Background(), selection, request, jev_api.ModelID, criteria)
	require.ErrorIs(t, err, errAutoModelDecisionRejected)
}

func TestAutoModelMiddlewareRewritesSingleCandidateAndRejectsAmbiguousModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := []service.Account{autoModelTestAccounts()[0], autoModelTestAccounts()[2]}
	h := newAutoModelTestHandler(accounts)
	group := &service.Group{ID: 71, Platform: service.PlatformComposite}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
		c.Next()
	}, h.AutoModelMiddleware(service.NewCompositeRouteResolver(nil)))
	router.POST("/v1/responses", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.String(http.StatusOK, gjson.GetBytes(body, "model").String())
	})
	request := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		return recorder
	}
	selected := request(`{"model":"auto","input":"write a parser"}`)
	require.Equal(t, http.StatusOK, selected.Code, selected.Body.String())
	require.Equal(t, "gpt-5.5", selected.Body.String())
	require.Equal(t, "gpt-5.5", selected.Header().Get("X-Sub2API-Selected-Model"))
	ambiguous := request(`{"model":"auto","model":"auto","input":"write a parser"}`)
	require.Equal(t, http.StatusBadRequest, ambiguous.Code)
}

func TestAutoModelRequestPathIncludesResponsesSubpaths(t *testing.T) {
	for _, path := range []string{"/v1/responses/*subpath", "/responses/*subpath", "/backend-api/codex/responses/*subpath"} {
		require.True(t, autoModelRequestPath(path), path)
	}
}

func TestAutoModelMiddlewareUsesSystemOneForMultipleCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	decisionCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decisionCalls++
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, jev_api.ModelID, gjson.GetBytes(body, "model").String())
		require.Equal(t, "write a parser", gjson.GetBytes(body, "state.request").String())
		require.True(t, gjson.GetBytes(body, "questions.model.criteria.gpt-5\\.5").Exists())
		_, _ = w.Write([]byte(`{"answers":{"model":{"choice":"deepseek-v4-flash"}}}`))
	}))
	defer upstream.Close()
	accounts := autoModelTestAccounts()
	accounts[2].Type = service.AccountTypeAPIKey
	accounts[2].Credentials["base_url"] = upstream.URL
	accounts[2].Credentials["api_key"] = "decision-key"
	h := newAutoModelTestHandler(accounts)
	group := &service.Group{ID: 71, Platform: service.PlatformComposite}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
		c.Next()
	}, h.AutoModelMiddleware(service.NewCompositeRouteResolver(nil)))
	router.POST("/v1/responses", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.String(http.StatusOK, gjson.GetBytes(body, "model").String())
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto","input":"write a parser"}`)))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "deepseek-v4-flash", recorder.Body.String())
	require.Equal(t, 1, decisionCalls)
}

func TestAutoModelOpenAIGroupUsesSystemOne(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"model":{"choice":"gpt-5.6-sol"}}}`))
	}))
	defer upstream.Close()
	accounts := autoModelTestAccounts()
	accounts[1].Platform = service.PlatformOpenAI
	accounts[1].Credentials["model_mapping"] = map[string]any{"gpt-5.6-sol": "gpt-5.6-sol"}
	accounts[2].Type = service.AccountTypeAPIKey
	accounts[2].Credentials["base_url"] = upstream.URL
	h := newAutoModelTestHandler(accounts)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
		c.Next()
	}, h.AutoModelMiddleware(nil))
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.String(http.StatusOK, gjson.GetBytes(body, "model").String())
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto","messages":[{"role":"user","content":"write a parser"}]}`)))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "gpt-5.6-sol", recorder.Body.String())
}

func TestAutoModelFallsBackToLayaDecisionAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, jev_api.LayaModelID, gjson.GetBytes(body, "model").String())
		_, _ = w.Write([]byte(`{"answers":{"model":{"choice":"gpt-5.5"}}}`))
	}))
	defer upstream.Close()
	accounts := autoModelTestAccounts()[:2]
	accounts = append(accounts, service.Account{
		ID: 4, Platform: service.PlatformLaya, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_protocol": service.APIProtocolSystemOne, "base_url": upstream.URL,
			"model_mapping": map[string]any{jev_api.LayaModelID: jev_api.LayaModelID}},
	})
	h := newAutoModelTestHandler(accounts)
	group := &service.Group{ID: 71, Platform: service.PlatformComposite}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto","input":"write a parser"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
	chosen, err := h.chooseAutoModel(c, &service.APIKey{GroupID: &group.ID, Group: group}, []byte(`{"model":"auto","input":"write a parser"}`), []string{"gpt-5.5", "deepseek-v4-flash"})
	require.NoError(t, err)
	require.Equal(t, "gpt-5.5", chosen)
}

func TestAutoModelStateKeepsTextAndOmitsImageData(t *testing.T) {
	body := []byte(`{"model":"auto","input":[{"role":"user","content":[{"type":"input_text","text":"explain this image"},{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}],"tools":[{"type":"function","name":"shell"}]}`)
	state := autoModelState(body)
	require.Equal(t, "explain this image", state["request"])
	require.Equal(t, "shell", state["tools"])
	require.NotContains(t, state["request"], "AAAA")
}
