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

func TestAutoModelStandardCatalogAdvertisesImages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformComposite} {
		t.Run(platform, func(t *testing.T) {
			h := newAutoModelTestHandler(autoModelTestAccounts())
			group := &service.Group{ID: 71, Platform: platform}
			request := func(model, etag string) *httptest.ResponseRecorder {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
				c.Request.Header.Set("If-None-Match", etag)
				if model != "" {
					c.Params = gin.Params{{Key: "model", Value: model}}
				}
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
				h.Models(c)
				return recorder
			}

			listing := request("", "")
			require.Equal(t, http.StatusOK, listing.Code, listing.Body.String())
			auto := gjson.GetBytes(listing.Body.Bytes(), `data.#(id=="auto")`)
			require.JSONEq(t, `["text","image"]`, auto.Get("input_modalities").Raw)
			require.True(t, auto.Get("supports_image_detail_original").Exists())
			require.False(t, auto.Get("supports_image_detail_original").Bool())
			require.NotEmpty(t, listing.Header().Get("ETag"))

			cached := request("", listing.Header().Get("ETag"))
			require.Equal(t, http.StatusNotModified, cached.Code)
			require.Empty(t, cached.Body.String())

			retrieved := request(autoModelID, listing.Header().Get("ETag"))
			require.Equal(t, http.StatusOK, retrieved.Code, retrieved.Body.String())
			require.JSONEq(t, auto.Raw, retrieved.Body.String())

			group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-5.5"}}
			excluded := request("", listing.Header().Get("ETag"))
			require.Equal(t, http.StatusOK, excluded.Code)
			require.False(t, gjson.GetBytes(excluded.Body.Bytes(), `data.#(id=="auto")`).Exists())
			require.Equal(t, http.StatusNotFound, request(autoModelID, "").Code)
		})
	}
}
