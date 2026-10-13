package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfiguredModelsFallbackAndPublicAliases(t *testing.T) {
	for _, status := range []int{404, 405, 401, 403, 429, 500, 200} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			gateway := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				response := ordinaryModelsUpstreamResponse(`{"error":"no catalog"}`)
				response.StatusCode = status
				return response, nil
			}})
			account := newCodexModelsAPIKeyTestAccount("https://provider.example/plan/v3")
			account.Extra = map[string]any{"openai_passthrough": true}
			account.Credentials["model_mapping"] = map[string]any{
				"tencent/auto": "auto", "deepseek-v4.1-flash": "deepseek/deepseek-flash",
				"duplicate": "deepseek/deepseek-flash", "wildcard": "upstream-*",
			}
			response, err := gateway.FetchOpenAIModelsList(context.Background(), account)
			if status != 404 && status != 405 {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, `{"object":"list","data":[
				{"id":"auto","object":"model","created":0,"owned_by":"account-configured"},
				{"id":"deepseek/deepseek-flash","object":"model","created":0,"owned_by":"account-configured"}]}`, string(response.Body))
			svc := &AccountTestService{openaiGatewayService: gateway}
			models, err := svc.FetchOpenAIAccountModels(context.Background(), account)
			require.NoError(t, err)
			ids := make([]string, 0, len(models))
			for _, model := range models {
				ids = append(ids, model.ID)
			}
			require.ElementsMatch(t, []string{"tencent/auto", "deepseek-v4.1-flash", "duplicate"}, ids)
			account.Credentials["model_mapping"] = map[string]any{"new-public": "new-target"}
			updated, err := gateway.FetchOpenAIModelsList(context.Background(), account)
			require.NoError(t, err)
			require.NotEqual(t, response.ETag, updated.ETag)
			require.Contains(t, string(updated.Body), "new-target")
			require.NotContains(t, string(updated.Body), "deepseek")
			account.Credentials["model_mapping"] = map[string]any{}
			_, err = gateway.FetchOpenAIModelsList(context.Background(), account)
			require.Error(t, err)
		})
	}
}

func TestConfiguredModelsFallbackWithoutSharedGateway(t *testing.T) {
	response := ordinaryModelsUpstreamResponse("404 page not found")
	response.StatusCode = http.StatusNotFound
	svc := &AccountTestService{httpUpstream: &httpUpstreamRecorder{resp: response}, cfg: upstreamModelSyncTestConfig()}
	account := newCodexModelsAPIKeyTestAccount("https://provider.example/plan/v3")
	account.Extra = map[string]any{"openai_passthrough": true}
	account.Credentials["model_mapping"] = map[string]any{"tencent/auto": "auto"}
	models, err := svc.FetchOpenAIAccountModels(context.Background(), account)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "tencent/auto", models[0].ID)
	encoded, err := json.Marshal(models)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"id":"auto"`)
}
