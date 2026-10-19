package handler

import (
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
				body := []byte(`{"` + shape.field + `":[{"` + shape.id + `":"glm-5.3"},{"` + shape.id + `":"deepseek-v4.1-flash","offline":true},{"` + shape.id + `":"gpt-6.1-sol"},{"` + shape.id + `":"gpt-6-astra"},{"` + shape.id + `":"auto"},{"` + shape.id + `":"ask"},{"` + shape.id + `":"tier-1"},{"` + shape.id + `":"translate"}]}`)
				request := func(model, etag string) *httptest.ResponseRecorder {
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
					c.Set(autoModelListingKey, true)
					c.Set(autoModelListingModelsKey, []string{virtualID})
					c.Set(autoModelListingETagKey, etag)
					if model != "" {
						c.Params = gin.Params{{Key: "model", Value: model}}
					}
					writeOpenAIModelsResponse(c, &service.OpenAIModelsResponse{Body: body})
					return rec
				}
				rec := request("", "")
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Len(t, gjson.GetBytes(rec.Body.Bytes(), shape.field).Array(), 7)
				require.True(t, gjson.GetBytes(rec.Body.Bytes(), shape.field+`.#(`+shape.id+`=="deepseek-v4.1-flash").offline`).Bool())
				require.Equal(t, http.StatusNotModified, request("", rec.Header().Get("ETag")).Code)
				require.Equal(t, http.StatusNotFound, request("glm-5.3", "").Code)
			})
		}
	}
}
