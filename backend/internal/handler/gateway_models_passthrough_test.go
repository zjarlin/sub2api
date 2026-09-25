package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayModelsPassthroughPreservesVerifiedModelsAndAliases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const canonical = "deepseek-v4.1-flash"
	const upstream = "deepseek/deepseek-v4.1-flash"
	const groupID = int64(6)
	policy := &service.ModelAliasPolicy{Groups: []service.ModelAliasGroup{
		{Canonical: canonical, Aliases: []string{upstream}},
	}}

	for _, tc := range []struct {
		name      string
		allowlist service.GroupModelAllowlist
		want      []string
	}{
		{name: "all verified models", want: []string{canonical, "gpt-6-astra"}},
		{
			name:      "group allowlist remains effective",
			allowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"deepseek/*"}},
			want:      []string{canonical},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newGatewayModelsHandlerForTest(
				&gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{
					groupID: {
						{
							ID: 851, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
							Extra: map[string]any{"openai_passthrough": true},
							Credentials: map[string]any{"model_mapping": map[string]any{
								upstream: upstream, canonical: canonical, "unverified-model": "unverified-model",
							}},
						},
						{
							ID: 820, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
							Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}},
						},
					},
					7: {{ID: 999, Platform: service.PlatformOpenAI}},
				}},
				&gatewayModelsHealthRepoStub{observations: []service.ModelHealthObservation{
					{AccountID: 851, Model: upstream},
					{AccountID: 851, Model: canonical},
					{AccountID: 820, Model: "gpt-6-astra"},
					{AccountID: 999, Model: "other-group-model"},
				}},
			)
			for _, modelID := range []string{"", canonical} {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
				c.Request = c.Request.WithContext(service.WithModelAliases(c.Request.Context(), policy))
				c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{
					ID: groupID, Platform: service.PlatformOpenAI, ModelAllowlist: tc.allowlist,
				}})
				if modelID != "" {
					c.Params = gin.Params{{Key: "model", Value: modelID}}
				}

				h.Models(c)

				require.Equal(t, http.StatusOK, rec.Code)
				if modelID != "" {
					var got gatewayModelItemForTest
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
					require.Equal(t, canonical, got.ID)
					continue
				}
				var got gatewayModelsResponseForTest
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
				require.Equal(t, "list", got.Object)
				require.ElementsMatch(t, tc.want, modelIDsForTest(got.Data))
			}
		})
	}
}
