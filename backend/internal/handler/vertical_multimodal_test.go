//go:build unit

package handler

import (
	"encoding/base64"
	"encoding/json"
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
	"github.com/tidwall/gjson"
)

func TestVerticalForcedImageToolAndInputBoundaries(t *testing.T) {
	for _, test := range []struct {
		body   string
		forced bool
		text   string
	}{
		{`{"input":"a cat","tools":[{"type":"image_generation"}],"tool_choice":{"type":"image_generation"}}`, true, "a cat"},
		{`{"input":"a cat","tools":[{"type":"image_generation"}],"tool_choice":"required"}`, true, "a cat"},
		{`{"input":"explain APIs","tools":[{"type":"image_generation"}]}`, false, "explain APIs"},
		{`{"input":"a cat","tools":[{"type":"image_generation","action":"edit"}],"tool_choice":"required"}`, false, ""},
		{`{"input":"a cat","tools":[{"type":"image_generation"},{"type":"web_search"}],"tool_choice":"required"}`, false, ""},
		{`{"input":[{"role":"user","content":[{"type":"input_text","text":"edit image"},{"type":"input_image","image_url":"https://example.com/a.png"}]}],"tools":[{"type":"image_generation"}],"tool_choice":"required"}`, true, ""},
		{`{"input":"a cat","previous_response_id":"resp_1","tools":[{"type":"image_generation"}],"tool_choice":"required"}`, true, ""},
	} {
		require.Equal(t, test.forced, verticalForcedImageTool([]byte(test.body)), test.body)
		require.Equal(t, test.text, verticalUserText([]byte(test.body)), test.body)
	}
}

func TestVerticalWanDirectChatHonorsGroupImagePermission(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"wan2.7-image","messages":[{"role":"user","content":[{"type":"text","text":"a cat"}]}]}`))
	key := &service.APIKey{ID: 81, Group: &service.Group{ID: 71, Platform: service.PlatformOpenAI}, User: &service.User{ID: 3}}
	c.Set(string(middleware.ContextKeyAPIKey), key)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}, billingCacheService: &service.BillingCacheService{}, apiKeyService: &service.APIKeyService{}, concurrencyHelper: &ConcurrencyHelper{concurrencyService: &service.ConcurrencyService{}}, cfg: &config.Config{}}
	h.ChatCompletions(c)
	require.Equal(t, http.StatusForbidden, c.Writer.Status())
}

func TestVerticalImageOptionsPreservePrompt(t *testing.T) {
	request := verticalMediaRequest("gpt-image-1", "original prompt", "image_generation")
	body := []byte(`{"instructions":"do not forward this","tools":[{"type":"image_generation","size":"1024x1024","output_format":"jpeg","quality":"high"}]}`)
	result, err := verticalImageOptions(request, body, false)
	require.NoError(t, err)
	require.Equal(t, "original prompt", gjson.GetBytes(result, "prompt").String())
	require.Equal(t, "jpeg", gjson.GetBytes(result, "output_format").String())
	require.Equal(t, "1024x1024", gjson.GetBytes(result, "size").String())
	require.False(t, gjson.GetBytes(result, "instructions").Exists())
	_, err = verticalImageOptions(request, body, true)
	require.Error(t, err)
}

func TestVerticalNativeImageStreamLifecycle(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	encoded := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n"))
	writeVerticalReply(c, []byte(`{"stream":true}`), "ask", "图片已生成。", []map[string]any{{"type": "image_generation_call", "status": "completed", "result": encoded, "output_format": "png"}})
	var imageEvents []string
	sequence := 0
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		value := gjson.Parse(strings.TrimPrefix(line, "data: "))
		require.Equal(t, int64(sequence), value.Get("sequence_number").Int())
		sequence++
		if value.Get("output_index").Int() != 1 {
			continue
		}
		imageEvents = append(imageEvents, value.Get("type").String())
		if value.Get("type").String() == "response.output_item.added" {
			require.Equal(t, "in_progress", value.Get("item.status").String())
			require.Empty(t, value.Get("item.result").String())
		}
		if value.Get("type").String() == "response.output_item.done" {
			require.Equal(t, encoded, value.Get("item.result").String())
		}
	}
	require.Equal(t, []string{"response.output_item.added", "response.image_generation_call.in_progress", "response.image_generation_call.generating", "response.image_generation_call.completed", "response.output_item.done"}, imageEvents)
}

func TestVerticalWanUsesChatHandlerAndPreservesParentInput(t *testing.T) {
	cache := &observationCache{}
	h := observationHandler(cache)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	key := &service.APIKey{ID: 81, GroupID: &group.ID, Group: group}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	parent := `{"model":"ask","instructions":"original","input":"a cat"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(parent))
	c.Set(string(middleware.ContextKeyAPIKey), key)
	images := func(*gin.Context) { t.Fatal("Wan must not call Images handler") }
	chats := func(child *gin.Context) {
		require.Equal(t, "/v1/chat/completions", child.Request.URL.Path)
		body, err := io.ReadAll(child.Request.Body)
		require.NoError(t, err)
		require.Equal(t, "a cat", gjson.GetBytes(body, "messages.0.content.0.text").String())
		require.False(t, gjson.GetBytes(body, "instructions").Exists())
		child.JSON(200, json.RawMessage(`{"output":{"choices":[{"message":{"content":[{"type":"image","image":"https://cdn.example.com/cat.png"}]}}]}}`))
	}
	h.executeVerticalMedia(c, key, "ask", []byte(parent), "a cat", "image_generation", "wan2.7-image", service.PlatformOpenAI, service.NewCompositeRouteResolver(nil), images, nil, chats)
	require.Equal(t, 200, recorder.Code)
	body, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.Equal(t, parent, string(body))
	require.Equal(t, "/v1/chat/completions", c.Request.URL.Path)
}

func TestVerticalImageFallbackOnlyRetriesCapacityAndServerErrors(t *testing.T) {
	for _, failure := range []int{429, 503, 400, 401} {
		t.Run(http.StatusText(failure), func(t *testing.T) {
			cache := &observationCache{}
			h := observationHandler(cache)
			inventory := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{71: {{ID: 1, Platform: service.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-1": "gpt-image-1", "wan2.7-image": "wan2.7-image"}}}}}})
			h.gatewayService = inventory.gatewayService
			h.settingService = service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
				service.SettingKeyAutoModelPolicy: `{"blacklist":[],"vertical_routing":{"enabled":true,"min_confidence":0.8,"timeout_ms":1500,"image_fallback_models":["missing-image","wan2.7-image"]}}`,
			}}, nil)
			group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
			key := &service.APIKey{ID: 81, GroupID: &group.ID, Group: group}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c.Set(string(middleware.ContextKeyAPIKey), key)
			var imageCalls, chatCalls int
			images := func(child *gin.Context) {
				imageCalls++
				child.JSON(failure, gin.H{"error": gin.H{"message": "failed"}})
			}
			chats := func(child *gin.Context) {
				chatCalls++
				child.JSON(200, json.RawMessage(`{"output":{"choices":[{"message":{"content":[{"type":"image","image":"https://cdn.example.com/cat.png"}]}}]}}`))
			}
			h.executeVerticalMedia(c, key, "ask", []byte(`{}`), "a cat", "image_generation", "gpt-image-1", service.PlatformOpenAI, service.NewCompositeRouteResolver(nil), images, nil, chats)
			require.Equal(t, 1, imageCalls)
			if failure == 429 || failure >= 500 {
				require.Equal(t, 1, chatCalls)
				require.Equal(t, 200, recorder.Code)
				require.Equal(t, "wan2.7-image", recorder.Header().Get("X-Sub2API-Selected-Model"))
			} else {
				require.Zero(t, chatCalls)
				require.Equal(t, failure, recorder.Code)
			}
		})
	}
}

func TestVerticalForcedToolRunsWithoutClassifierOrPromptInjection(t *testing.T) {
	cache := &observationCache{}
	h := observationHandler(cache)
	inventory := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{71: {{ID: 1, Platform: service.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-1": "gpt-image-1"}}}}}})
	h.gatewayService = inventory.gatewayService
	h.settingService = service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{}}, nil)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI, AllowImageGeneration: true}
	key := &service.APIKey{ID: 81, GroupID: &group.ID, Group: group, User: &service.User{ID: 3}}
	images := func(child *gin.Context) {
		body, err := io.ReadAll(child.Request.Body)
		require.NoError(t, err)
		require.Equal(t, "a cat", gjson.GetBytes(body, "prompt").String())
		require.False(t, gjson.GetBytes(body, "instructions").Exists())
		encoded := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n"))
		child.JSON(200, gin.H{"data": []gin.H{{"b64_json": encoded}}})
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
	}, h.VerticalIntentMiddleware(nil, images, nil))
	router.POST("/v1/responses", func(*gin.Context) { t.Fatal("forced tool should short circuit") })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"ask","input":"a cat","instructions":"original","tools":[{"type":"image_generation","model":"gpt-image-1"}],"tool_choice":{"type":"image_generation"}}`)))
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	require.Equal(t, "image_generation_call", gjson.Get(recorder.Body.String(), "output.1.type").String())
	group.AllowImageGeneration = false
	denied := httptest.NewRecorder()
	router.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"ask","input":"a cat","tools":[{"type":"image_generation"}],"tool_choice":"required"}`)))
	require.Equal(t, http.StatusForbidden, denied.Code)
	group.AllowImageGeneration = true
	fallback := httptest.NewRecorder()
	router.ServeHTTP(fallback, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"ask","input":"a cat","tools":[{"type":"image_generation","model":"missing-image"}],"tool_choice":"required"}`)))
	require.Equal(t, http.StatusOK, fallback.Code, fallback.Body.String())
	h.gatewayService = newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{}).gatewayService
	unavailable := httptest.NewRecorder()
	router.ServeHTTP(unavailable, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"ask","input":"a cat","tools":[{"type":"image_generation"}],"tool_choice":"required"}`)))
	require.Equal(t, http.StatusServiceUnavailable, unavailable.Code)
}
