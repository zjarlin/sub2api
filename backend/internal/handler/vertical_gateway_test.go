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
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestVerticalTranslationExtractsOnlyProvidedText(t *testing.T) {
	for _, test := range []struct{ input, source, target string }{
		{"翻译成英文：你好，世界。", "你好，世界。", "en"},
		{"请把你好翻译成英文", "你好", "en"},
		{"把下面的内容翻译成日文：第一行\n第二行", "第一行\n第二行", "ja"},
		{"Translate hello world into Chinese", "hello world", "zh"},
		{"Please translate to French:\nhello", "hello", "fr"},
		{"翻译成英文：如何实现翻译接口？", "如何实现翻译接口？", "en"},
	} {
		t.Run(test.input, func(t *testing.T) {
			require.Equal(t, "translation", verticalExplicitKind(test.input))
			request := verticalTranslationRequest(test.input)
			require.NotNil(t, request)
			require.Equal(t, []string{test.source}, request.Text)
			require.Equal(t, test.target, request.TargetLang)
		})
	}
	for _, text := range []string{
		"翻译成英文", "翻译成英文然后解释语法：你好", "如何实现翻译成英文：你好", "把这个翻译成英文",
		"Translate this into English", "不要生成图片，解释图片生成接口", "生成视频接口怎么实现", "读取文件然后翻译成英文",
		"Generate an image and explain the code", "Generate a video then modify my project",
		"生成一张图片需要多少钱", "生成一张图片可以吗", "生成一张图片和一段视频",
		"生成图片：一座山，然后修改项目代码", "Generate an image: a mountain and explain its code",
	} {
		require.Empty(t, verticalExplicitKind(text), text)
	}
}

func TestVerticalRequestDoesNotReplayToolsOrReadInjectedInstructions(t *testing.T) {
	for _, body := range []string{
		`{"input":[{"role":"user","content":"翻译成英文：你好"},{"type":"function_call_output","call_id":"old","output":"ok"}]}`,
		`{"messages":[{"role":"user","content":"翻译成英文：你好"},{"role":"assistant","content":"done"}]}`,
		`{"input":[{"role":"user","content":[{"type":"input_text","text":"生成图片"},{"type":"input_image","image_url":"https://example.com/a.png"}]}]}`,
		`{"input":"翻译成英文：你好","previous_response_id":"resp_old"}`,
		`{"input":"翻译成英文：你好","text":{"format":{"type":"json_schema"}}}`,
		`{"input":"翻译成英文：你好","text":{"format":{"type":"json_object"}}}`,
		`{"input":"翻译成英文：你好","tool_choice":"required"}`,
		`{"input":"翻译成英文：你好","tool_choice":{"type":"function","name":"translate"}}}`,
		`{"instructions":"翻译成英文：你好","input":[{"role":"developer","content":"Generate an image"}]}`,
	} {
		require.Empty(t, verticalUserText([]byte(body)), body)
	}
	text := verticalUserText([]byte(`{"instructions":"generate a video","input":[{"role":"developer","content":"generate an image"},{"role":"user","content":"hello"}]}`))
	require.Equal(t, "hello", text)
}

func TestVerticalLayaUsesChosenProbabilityNotEntropyConfidence(t *testing.T) {
	actual := []byte(`{"model":"laya-rl-agent","answers":{"intent":{"choice":"C","probabilities":{"A":0.0011,"B":0.1865,"C":0.811,"D":0.0013},"confidence":0.6398,"answer_confidence":0.811}}}`)
	// 实测模型返回校准选择概率和熵置信度，二者不能混用。
	require.Equal(t, "video_generation", verticalDecisionKind(actual, 0.8))
	require.Empty(t, verticalDecisionKind(actual, 0.9))
	for _, payload := range []string{
		`{"model":"unknown","answers":{"intent":{"choice":"A","probabilities":{"A":1,"B":0,"C":0,"D":0}}}}`,
		`{"model":"laya","answers":{"intent":{"choice":"A","probabilities":{"A":1}}}}`,
		`{"model":"laya","answers":{"intent":{"choice":"A","probabilities":{"A":1,"B":1,"C":0,"D":0}}}}`,
		`{"model":"laya","answers":{"intent":{"choice":"D","probabilities":{"A":0,"B":0,"C":0,"D":1}}}}`,
	} {
		require.Empty(t, verticalDecisionKind([]byte(payload), 0.8))
	}
}

func verticalTestHandler(t *testing.T, upstream string, cache *observationCache) *GatewayHandler {
	t.Helper()
	t.Setenv("TRANSLATE_FREE_PROVIDERS", "false")
	settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
		service.SettingKeyTranslateProviders: fmt.Sprintf(`{"enabled":true,"priority":["hymt","caiyun","google_web","tencent","baidu","youdao","mymemory","libretranslate"],"google_web":{"enabled":false},"hymt":{"enabled":true,"base_url":%q}}`, upstream),
	}}, nil)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	h := observationHandler(cache)
	h.settingService, h.billingCacheService = settings, billing
	return h
}

func TestVerticalTranslationShortCircuitsEveryModelAndPreservesProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "你好") {
			t.Error("translation source missing")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Hello"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	for _, endpoint := range []string{"/v1/responses", "/responses", "/backend-api/codex/responses", "/v1/chat/completions", "/chat/completions"} {
		for _, model := range []string{"ask", "auto", "gpt-6-astra", "custom-bundle"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", endpoint, model, stream), func(t *testing.T) {
					cache := &observationCache{}
					h := verticalTestHandler(t, upstream.URL, cache)
					group := &service.Group{ID: 71, Platform: service.PlatformComposite}
					key := &service.APIKey{ID: 81, GroupID: &group.ID, Group: group, User: &service.User{ID: 3}}
					normalCalls := 0
					router := gin.New()
					router.Use(func(c *gin.Context) {
						c.Set(string(middleware.ContextKeyAPIKey), key)
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
					}, h.VerticalIntentMiddleware(nil, nil, nil))
					router.POST(endpoint, func(c *gin.Context) { normalCalls++; c.String(200, "normal model") })
					body := fmt.Sprintf(`{"model":%q,"input":"翻译成英文：你好","messages":[{"role":"user","content":"翻译成英文：你好"}],"stream":%t}`, model, stream)
					request := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
					request.Header.Set("X-Codex-Turn-Metadata", `{"thread_id":"`+observationSession+`","turn_id":"turn-1"}`)
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					require.Equal(t, 200, response.Code, response.Body.String())
					require.Zero(t, normalCalls)
					require.Contains(t, response.Body.String(), "Hello")
					require.Equal(t, "translation", response.Header().Get("X-Sub2API-Capability"))
					last := cache.routes[len(cache.routes)-1]
					require.Equal(t, model, last.RequestedModel)
					require.Equal(t, "completed", last.State)
					require.Equal(t, "hymt", last.Operation.Provider)
					require.Equal(t, "en", last.Operation.TargetLanguage)
					if stream && !strings.Contains(endpoint, "chat") {
						require.Contains(t, response.Body.String(), `"content_index":0`)
						require.Contains(t, response.Body.String(), "event: response.completed")
					}
					stored, err := json.Marshal(cache.routes)
					require.NoError(t, err)
					require.NotContains(t, string(stored), "你好")
				})
			}
		}
	}
	require.Equal(t, int32(40), calls.Load())
}

func TestVerticalTranslationFailureDoesNotSilentlyCallTextModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer upstream.Close()
	cache := &observationCache{}
	h := verticalTestHandler(t, upstream.URL, cache)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	request := verticalTranslationRequest("翻译成英文：你好")
	h.executeVerticalTranslation(c, &service.APIKey{ID: 81, GroupID: &group.ID, Group: group}, "ask", []byte(`{"stream":false}`), request)
	require.Equal(t, http.StatusBadGateway, c.Writer.Status())
	require.Equal(t, "failed", cache.routes[len(cache.routes)-1].State)
}

func TestVerticalMediaInternalCallPreservesParentAndTaskLifecycle(t *testing.T) {
	cache := &observationCache{}
	h := observationHandler(cache)
	group := &service.Group{ID: 71, Platform: service.PlatformGrok}
	key := &service.APIKey{ID: 81, GroupID: &group.ID, Group: group}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"ask"}`))
	c.Set(string(middleware.ContextKeyAPIKey), key)
	before, _ := io.ReadAll(c.Request.Body)
	c.Request.Body = io.NopCloser(strings.NewReader(string(before)))
	h.executeVerticalMedia(c, key, "ask", []byte(`{}`), "Generate a video", "video_generation", "grok-imagine-video", service.PlatformGrok,
		service.NewCompositeRouteResolver(nil), nil, func(child *gin.Context) {
			require.Equal(t, "/v1/videos/generations", child.Request.URL.Path)
			childKey, _ := middleware.GetAPIKeyFromContext(child)
			require.Same(t, key, childKey)
			child.JSON(200, gin.H{"request_id": "video-123", "status": "pending"})
		})
	require.Equal(t, "/v1/responses", c.Request.URL.Path)
	parentBody, _ := io.ReadAll(c.Request.Body)
	require.Equal(t, before, parentBody)
	last := cache.routes[len(cache.routes)-1]
	require.Equal(t, "queued", last.State)
	require.Equal(t, "video-123", last.Operation.TaskID)
	require.Contains(t, response.Body.String(), "尚未生成完成")
	applyVerticalVideoResult(&last, []byte(`{"status":"processing"}`))
	require.Equal(t, "running", last.State)
	applyVerticalVideoResult(&last, []byte(`{"status":"done","video":{"url":"https://cdn.example.com/result.mp4"}}`))
	require.Equal(t, "completed", last.State)
	require.Equal(t, "video", last.Operation.Artifacts[0].Kind)
	applyVerticalVideoResult(&last, []byte(`{"status":"failed"}`))
	require.Equal(t, "failed", last.State)
}

func TestVerticalMediaDoesNotAllowUnsafeArtifactURLs(t *testing.T) {
	for _, location := range []string{"javascript:alert(1)", "file:///etc/passwd", "https://secret@example.com/video", "https://example.com/<script>"} {
		require.Empty(t, verticalArtifactURL(location))
	}
	require.Equal(t, "https://example.com/video.mp4", verticalArtifactURL("https://example.com/video.mp4"))
}

func TestVerticalDashScopeImageRequestAndRecognition(t *testing.T) {
	for _, model := range []string{"wan2.7-image", "wan2.7-image-pro", "wanx2.1-image", "vendor/wan2.7-image"} {
		require.True(t, service.IsDashScopeChatImageModel(model), model)
	}
	for _, model := range []string{"gpt-image-2", "grok-imagine-image", "wan2.7-video", "qwen3.8-max", ""} {
		require.False(t, service.IsDashScopeChatImageModel(model), model)
	}
	body := verticalDashScopeImageRequest("wan2.7-image", "一只戴墨镜的柴犬")
	require.Equal(t, "wan2.7-image", gjson.GetBytes(body, "model").String())
	require.Equal(t, "text", gjson.GetBytes(body, "messages.0.content.0.type").String())
	require.Equal(t, "一只戴墨镜的柴犬", gjson.GetBytes(body, "messages.0.content.0.text").String())
}

func TestVerticalDashScopeImageReplyParsesArtifacts(t *testing.T) {
	cache := &observationCache{}
	h := observationHandler(cache)
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	key := &service.APIKey{ID: 81, GroupID: &group.ID, Group: group}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"ask","input":"生成一张图片"}`))
	c.Set(string(middleware.ContextKeyAPIKey), key)
	route, finish := h.verticalObservation(c, key, "ask", "wan2.7-image", &service.VerticalOperation{Kind: "image_generation", Provider: service.PlatformOpenAI})
	defer finish()
	result := []byte(`{"model":"wan2.7-image","output":{"choices":[{"message":{"content":[{"type":"image","image":"https://cdn.example.com/a.png"}]}}]},"usage":{"image_count":1}}`)
	h.finishVerticalDashScopeImage(c, key, "ask", []byte(`{"stream":false}`), route, "wan2.7-image", result)
	require.Equal(t, "completed", route.State)
	require.Len(t, route.Operation.Artifacts, 1)
	require.Equal(t, "https://cdn.example.com/a.png", route.Operation.Artifacts[0].URL)
	require.Contains(t, response.Body.String(), "https://cdn.example.com/a.png")
	require.NotContains(t, response.Body.String(), "cdn.example.com/a.png?secret")
}

func TestVerticalMediaModelRequiresInventoryAndHonorsAliasesAndAllowlist(t *testing.T) {
	accounts := []service.Account{{ID: 1, Platform: service.PlatformOpenAI, Credentials: map[string]any{
		"model_mapping": map[string]any{"vendor/gpt-image-2": "gpt-image-2", "agnes-image-2.0-flash": "agnes-image-2.0-flash"},
	}}}
	h := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{71: accounts}})
	ctx := service.WithModelAliases(context.Background(), &service.ModelAliasPolicy{Groups: []service.ModelAliasGroup{
		{Canonical: "gpt-image-2", Aliases: []string{"vendor/gpt-image-2"}},
	}})
	for _, test := range []struct {
		name, configured, want string
		allowlist              service.GroupModelAllowlist
	}{
		{name: "inventory discovery excludes unsupported image names", want: "gpt-image-2"},
		{name: "configured alias", configured: "vendor/gpt-image-2", want: "gpt-image-2"},
		{name: "unsupported configured model", configured: "agnes-image-2.0-flash"},
		{name: "missing model", configured: "missing-image"},
		{name: "disallowed model", configured: "gpt-image-2", allowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"ask"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			group := &service.Group{ID: 71, Platform: service.PlatformComposite, ModelAllowlist: test.allowlist}
			model, platform, err := h.verticalMediaModel(ctx, group, service.NewCompositeRouteResolver(nil), "image_generation", service.VerticalRoutingPolicy{ImageModel: test.configured})
			require.NoError(t, err)
			require.Equal(t, test.want, model)
			if test.want != "" {
				require.Equal(t, service.PlatformOpenAI, platform)
			}
		})
	}
}

func TestVerticalVideoRefreshUsesTaskProviderInsteadOfOriginalGroup(t *testing.T) {
	cache := &observationCache{}
	h := observationHandler(cache)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/auto/routes", nil)
	key := &service.APIKey{ID: 81, Group: &service.Group{ID: 71, Platform: service.PlatformOpenAI}}
	c.Set(string(middleware.ContextKeyAPIKey), key)
	routes := []service.AutoModelRouteObservation{{
		State: "queued", UpdatedAt: time.Now().Add(-5 * time.Second).UnixMilli(),
		Operation: &service.VerticalOperation{Kind: "video_generation", Provider: service.PlatformGrok, TaskID: "video-123"},
	}}
	h.refreshVerticalVideos(c, key, cache, routes, func(child *gin.Context) {
		platform, ok := service.ResolvedTargetPlatformFromContext(child.Request.Context())
		require.True(t, ok)
		require.Equal(t, service.PlatformGrok, platform)
		childKey, _ := middleware.GetAPIKeyFromContext(child)
		require.Same(t, key, childKey)
		require.Equal(t, "video-123", child.Param("request_id"))
		child.JSON(200, gin.H{"status": "done", "video": gin.H{"url": "https://example.com/video.mp4"}})
	})
	require.Equal(t, "completed", routes[0].State)
	require.Equal(t, "completed", cache.routes[len(cache.routes)-1].State)
}
