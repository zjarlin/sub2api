package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPackagedModelCatalogIncludesOfflineModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformComposite} {
		t.Run(platform, func(t *testing.T) {
			accounts := autoModelTestAccounts()
			accounts[0].Schedulable = false
			accounts[1].Schedulable = false
			accounts[0].Credentials = map[string]any{"model_mapping": map[string]any{
				"gpt-6.1-sol": "gpt-6.1-sol", "gpt-6-astra": "gpt-6-astra", "gpt-5.5": "gpt-5.5",
			}}
			accounts[1].Credentials = map[string]any{"model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}}
			h := newAutoModelTestHandler(accounts)
			group := &service.Group{ID: 71, Platform: platform}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			h.MarkPackagedCatalogListing(c, group)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
			h.Models(c)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			for _, id := range []string{"auto", "ask", "gpt-6.1-sol", "gpt-6-astra", "deepseek-v4.1-flash"} {
				require.True(t, gjson.GetBytes(rec.Body.Bytes(), `data.#(id=="`+id+`")`).Exists(), rec.Body.String())
			}
			require.False(t, gjson.GetBytes(rec.Body.Bytes(), `data.#(id=="gpt-5.5")`).Exists(), rec.Body.String())
		})
	}
}

func TestPackagedCatalogFiltersBothShapesAndVirtualEntrypoints(t *testing.T) {
	for _, virtualID := range []string{autoModelID, askModelID} {
		for _, shape := range []struct{ field, id string }{{"data", "id"}, {"models", "slug"}} {
			t.Run(virtualID+"/"+shape.field, func(t *testing.T) {
				body := []byte(`{"` + shape.field + `":[{"` + shape.id + `":"glm-5.3"},{"` + shape.id + `":"deepseek-v4.1-flash","offline":true},{"` + shape.id + `":"gpt-6.1-sol"},{"` + shape.id + `":"gpt-6-astra"},{"` + shape.id + `":"q3-4b"},{"` + shape.id + `":"auto"},{"` + shape.id + `":"ask"},{"` + shape.id + `":"free"},{"` + shape.id + `":"translate"},{"` + shape.id + `":"tier-1"},{"` + shape.id + `":"gpt-image-1"}]}`)
				request := func(model, etag string) *httptest.ResponseRecorder {
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
					c.Set(autoModelListingKey, true)
					c.Set(autoModelListingModelsKey, []string{virtualID})
					c.Set(autoModelListingETagKey, etag)
					c.Set(packagedCatalogVisibleKey, []string{"deepseek-v4.1-flash", "gpt-6.1-sol", "gpt-6-astra", "q3-4b"})
					if model != "" {
						c.Params = gin.Params{{Key: "model", Value: model}}
					}
					writeOpenAIModelsResponse(c, &service.OpenAIModelsResponse{Body: body})
					return rec
				}
				rec := request("", "")
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Len(t, gjson.GetBytes(rec.Body.Bytes(), shape.field).Array(), 8)
				require.True(t, gjson.GetBytes(rec.Body.Bytes(), shape.field+`.#(`+shape.id+`=="deepseek-v4.1-flash").offline`).Bool())
				require.Equal(t, http.StatusNotModified, request("", rec.Header().Get("ETag")).Code)
				require.Equal(t, http.StatusNotFound, request("glm-5.3", "").Code)
				require.Equal(t, http.StatusNotFound, request("tier-1", "").Code)
				require.Equal(t, http.StatusNotFound, request("gpt-image-1", "").Code)
			})
		}
	}
}

func TestPackagedFreeEntrypointOnlyUsesFreeModels(t *testing.T) {
	accounts := []service.Account{
		{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}},
		{ID: 2, Platform: service.PlatformKilo, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"kilo-auto/free": "kilo-auto/free"}}},
	}
	h := newAutoModelTestHandler(accounts)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	ctx, err := h.settingService.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx, models, err := h.gatewayService.BindAutoModelInventory(ctx, group.ID)
	require.NoError(t, err)
	body := []byte(`{"model":"free","input":"hello"}`)
	ctx = service.WithAutoModelRequestCapabilities(ctx, body)
	routes, plan, err := h.autoModelPlan(ctx, group, nil, "/v1/responses", body, models)
	require.NoError(t, err)
	require.Equal(t, []string{"kilo-auto/free"}, modelIDsFromAutoRoutes(routes))
	for _, candidate := range plan {
		if candidate.Eligible {
			require.True(t, service.AutoModelFreeModelID(candidate.Model), candidate.Model)
		}
	}
	require.True(t, h.freeVirtualModelAvailable(context.Background(), group, models))
	require.True(t, containsString(h.availableVirtualModelIDs(context.Background(), group, models), freeModelID))
}

func gotModelsForTest(t *testing.T, rec *httptest.ResponseRecorder) []gatewayModelItemForTest {
	t.Helper()
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	return got.Data
}
