package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestStripAskToolDeclarationsKeepsHostedSearch(t *testing.T) {
	body := []byte(`{"model":"ask","tool_choice":{"type":"function","name":"shell"},"parallel_tool_calls":true,"tools":[{"type":"web_search"},{"type":"function","name":"shell","parameters":{"type":"object"}},{"type":"namespace","name":"ns","tools":[{"type":"web_search_preview"},{"type":"function","name":"exec"}]}],"input":[{"type":"additional_tools","tools":[{"type":"web_search_preview_2025_03_11"},{"type":"function","name":"other"}]}]}`)
	converted, err := stripAskToolDeclarations(body)
	require.NoError(t, err)
	require.Equal(t, "web_search", gjson.GetBytes(converted, "tools.0.type").String())
	require.Equal(t, "web_search_preview", gjson.GetBytes(converted, "tools.1.tools.0.type").String())
	require.Equal(t, "web_search_preview_2025_03_11", gjson.GetBytes(converted, "input.0.tools.0.type").String())
	require.False(t, gjson.GetBytes(converted, "tools.1.tools.1").Exists())
	require.False(t, gjson.GetBytes(converted, "tool_choice").Exists())
	require.True(t, gjson.GetBytes(converted, "parallel_tool_calls").Bool())
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
		{name: "decision account missing", accounts: autoModelTestAccounts()[:2], wantAuto: true},
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
		if model.Slug == autoModelID {
			require.Equal(t, []string{"text", "image"}, model.InputModalities)
		}
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
	require.True(t, containsModelID(ordinaryCatalog.Data, askModelID))

	manifestBody := []byte(`{"models":[{"slug":"gpt-5.5","visibility":"list"}]}`)
	manifest := func(c *gin.Context) {
		h.PrepareAutoModelListing(c)
		writeOpenAIModelsResponse(c, &service.OpenAIModelsResponse{Body: manifestBody})
	}
	first := request("/v1/models?client_version=1.0", "", manifest)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.Contains(t, first.Body.String(), `"slug":"auto"`)
	require.Equal(t, `["text","image"]`, gjson.Get(first.Body.String(), `models.#(slug=="auto").input_modalities`).Raw)
	require.NotEmpty(t, first.Header().Get("ETag"))
	notModified := request("/v1/models?client_version=1.0", first.Header().Get("ETag"), manifest)
	require.Equal(t, http.StatusNotModified, notModified.Code)
	require.Empty(t, notModified.Body.String())
}

func TestAutoModelPinnedOpenAICatalogUsesDiscoveredModels(t *testing.T) {
	accounts := autoModelTestAccounts()
	accounts[0] = newPinnedCodexAccount(1, service.StatusActive, true, false)
	accounts[0].Credentials["model_mapping"] = map[string]any{"gpt-5.5": "gpt-5.5", "gpt-image-1": "gpt-image-1"}
	upstream := &codexModelsPinnedHTTPUpstream{bodies: map[int64]string{
		1: `{"data":[{"id":"gpt-5.5"},{"id":"gpt-image-1"}]}`,
	}}
	h := newAutoModelTestHandler(accounts)
	h.openAIGatewayService = newPinnedCodexTestHandler(accounts, upstream, 3).gatewayService
	h.maxAccountSwitches = 3
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI,
		CodexModelsManifestConfig: service.GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{1}}}
	require.ElementsMatch(t, []string{"gpt-5.5", "deepseek-v4-flash"}, autoModelCandidatesForGroup(group, h.autoModelCatalog(context.Background(), group)))
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

func TestAppendAutoModelUpdatesExistingVisionCapability(t *testing.T) {
	for _, tc := range []struct{ field, idField string }{{"models", "slug"}, {"data", "id"}} {
		t.Run(tc.field, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{tc.field: []map[string]any{
				{tc.idField: "auto", "input_modalities": []string{"text"}, "supports_image_detail_original": true, "context_window": 100000},
				{tc.idField: "text-model", "input_modalities": []string{"text"}},
			}})
			require.NoError(t, err)
			updated, err := appendAutoModelToCatalog(body)
			require.NoError(t, err)
			require.Equal(t, int64(2), gjson.GetBytes(updated, tc.field+".#").Int())
			require.Equal(t, `["text","image"]`, gjson.GetBytes(updated, tc.field+".0.input_modalities").Raw)
			require.False(t, gjson.GetBytes(updated, tc.field+".0.supports_image_detail_original").Bool())
			require.Equal(t, int64(100000), gjson.GetBytes(updated, tc.field+".0.context_window").Int())
			require.Equal(t, gjson.GetBytes(body, tc.field+".1").Raw, gjson.GetBytes(updated, tc.field+".1").Raw)
		})
	}
}

func TestAskModelStripsToolDeclarations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newAutoModelTestHandler(autoModelTestAccounts())
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: group})
	}, h.AutoModelMiddleware(nil))
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{
			name: "chat completions",
			path: "/v1/chat/completions",
			body: `{"model":"ask","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup"}}],"tool_choice":"auto","parallel_tool_calls":true}`,
		},
		{
			name: "responses",
			path: "/v1/responses",
			body: `{"model":"ask","input":"hi","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"tool_choice":"auto","parallel_tool_calls":true}`,
		},
	} {
		router.POST(tc.path, func(c *gin.Context) {
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			c.Data(http.StatusOK, "application/json", body)
		})
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, "deepseek-v4-flash", gjson.Get(recorder.Body.String(), "model").String())
			require.False(t, gjson.Get(recorder.Body.String(), "tools").Exists())
			require.False(t, gjson.Get(recorder.Body.String(), "tool_choice").Exists())
			require.False(t, gjson.Get(recorder.Body.String(), "parallel_tool_calls").Exists())
		})
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
	require.Equal(t, []string{"auto", "ask", "gpt-5.5"}, prependAutoModel([]string{"gpt-5.5", "auto", "ask"}))
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
	accounts[1].Schedulable = false
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
	require.Equal(t, "gpt-tool-ready-a", recorder.Body.String())
	require.Equal(t, 0, decisionCalls)
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

func TestAutoModelContinuesAfterEncryptedReasoningToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := []service.Account{autoModelTestAccounts()[0], autoModelTestAccounts()[2]}
	accounts[0].Type = service.AccountTypeAPIKey
	accounts[0].Extra = map[string]any{"openai_responses_supported": true}
	h := newAutoModelTestHandler(accounts)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	key := &service.APIKey{GroupID: &group.ID, Group: group}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), key)
	}, h.AutoModelMiddleware(nil))
	var forwarded [][]byte
	router.POST("/responses", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		model := gjson.GetBytes(body, "model").String()
		selection := &service.AccountSelectionResult{Account: &accounts[0]}
		require.False(t, rejectIncompatibleModelFallbackAccount(c, selection, model, body, true))
		forwarded = append(forwarded, body)
		if len(forwarded) == 2 {
			require.True(t, modelFallbackReplayableRequest(c, key, model, body))
			// 已确认的 Responses→Chat 桥接账号可以继续承接 Auto 请求。
			chatAccount := accounts[0]
			chatAccount.Extra = map[string]any{"openai_responses_supported": false}
			released := false
			require.False(t, rejectIncompatibleModelFallbackAccount(c, &service.AccountSelectionResult{
				Account: &chatAccount, ReleaseFunc: func() { released = true },
			}, model, body, true))
			require.False(t, released)
		}
		c.JSON(http.StatusOK, gin.H{"model": model})
	})
	history := `[{"role":"user","content":"use shell"},{"type":"reasoning","summary":[{"type":"summary_text","text":"Inspecting files."}],"encrypted_content":"opaque"},{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]`
	for _, input := range []string{`"use shell"`, history} {
		body := `{"model":"auto","input":` + input + `,"tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}]}`
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(body)))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Equal(t, "gpt-5.5", recorder.Header().Get("X-Sub2API-Selected-Model"))
	}
	require.Len(t, forwarded, 2)
	require.JSONEq(t, history, gjson.GetBytes(forwarded[1], "input").Raw)
}

func TestAutoModelMiddlewarePrefersEconomicalModelWithoutSystemOne(t *testing.T) {
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
	require.Equal(t, 0, decisionCalls)
}

func TestAutoModelOpenAIGroupUsesCapabilityOrder(t *testing.T) {
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
