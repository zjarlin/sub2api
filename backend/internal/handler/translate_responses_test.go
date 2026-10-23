package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTranslateResponsesOffline(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "zh", payload["target"])
		require.Equal(t, "Hello", payload["q"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"translatedText":"你好"}`))
	}))
	defer upstream.Close()
	h := NewTranslateHandler(translate.NewAggregator(&translate.Config{LibreTranslate: &translate.LibreTranslateConfig{BaseURL: upstream.URL}}), nil)
	for _, stream := range []string{"false", "true"} {
		router := gin.New()
		router.POST("/v1/responses", h.TranslateModelMiddleware(), func(c *gin.Context) { t.Error("translate reached normal scheduler") })
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"translate","input":"Hello","stream":`+stream+`}`))
		router.ServeHTTP(recorder, request)
		require.Equal(t, 200, recorder.Code)
		require.Equal(t, "libretranslate", recorder.Header().Get("X-Translation-Provider"))
		if stream == "true" {
			require.Contains(t, recorder.Body.String(), "event: response.output_text.delta")
			require.Contains(t, recorder.Body.String(), "event: response.completed")
		} else {
			var result map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
			require.Equal(t, "translate", result["model"])
			require.Equal(t, "completed", result["status"])
		}
		require.Contains(t, recorder.Body.String(), "你好")
	}
}

func TestTranslateResponsesInput(t *testing.T) {
	req, err := parseTranslateResponseInput([]byte(`{"input":[{"role":"user","content":[{"type":"input_text","text":"你好"}]}],"metadata":{"target_language":"en"}}`), "zh")
	require.NoError(t, err)
	require.Equal(t, "en", req.TargetLang)
	require.Equal(t, []string{"你好"}, req.Text)

	// Codex 会带上客户端能力字段，并把 environment_context 作为 user 消息注入。
	// 只应翻译最后一条 user 消息，客户端字段不应导致 400。
	req, err = parseTranslateResponseInput([]byte(`{
		"input": [
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"You are Codex"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>...</environment_context>"}]},
			{"type":"reasoning","summary":[]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Hello"}]}
		],
		"tools": [{"type":"function","name":"exec_command"}],
		"tool_choice": "auto",
		"parallel_tool_calls": true,
		"reasoning": {"effort":"none"},
		"include": ["reasoning.encrypted_content"],
		"prompt_cache_key": "thread-1",
		"client_metadata": {"thread_id":"thread-1"},
		"store": false
	}`), "zh")
	require.NoError(t, err)
	require.Equal(t, []string{"Hello"}, req.Text)

	for _, body := range []string{
		`{"input":""}`, `{"input":null}`, `{"input":42}`,
		`{"input":"Hi","previous_response_id":"resp_old"}`,
		`{"input":"Hi","conversation":{"id":"conv_old"}}`,
		`{"input":"Hi","background":true}`,
		`{"input":"Hi","store":true}`,
		`{"input":[{"role":"user","content":[{"type":"input_image"}]}]}`,
		`{"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"Translate"}]}]}`,
	} {
		_, err := parseTranslateResponseInput([]byte(body), "zh")
		require.Error(t, err, body)
	}
}

// Codex 的翻译请求不会带 metadata：英文输入应译成中文，中文输入不该原样返回。
func TestTranslateResponsesInfersTargetLang(t *testing.T) {
	req, err := parseTranslateResponseInput([]byte(`{"input":"Hello world"}`), "zh")
	require.NoError(t, err)
	require.Equal(t, "zh", req.TargetLang)

	req, err = parseTranslateResponseInput([]byte(`{"input":"今天天气不错，我们去散步吧。"}`), "zh")
	require.NoError(t, err)
	require.Equal(t, "en", req.TargetLang)

	// 显式目标语言优先于推断。
	req, err = parseTranslateResponseInput([]byte(`{"input":"你好","metadata":{"target_language":"ja"}}`), "zh")
	require.NoError(t, err)
	require.Equal(t, "ja", req.TargetLang)

	// 中英混排但以中文为主时按中文处理。
	require.Equal(t, "en", pickTargetLang([]string{"这个 README 里的 example 需要更新"}, "zh"))
	require.Equal(t, "zh", pickTargetLang([]string{"Update the README example please"}, "zh"))
}

func TestTranslateResponsesPassesOtherModels(t *testing.T) {
	h := NewTranslateHandler(nil, nil)
	router := gin.New()
	router.POST("/v1/responses", h.TranslateModelMiddleware(), func(c *gin.Context) {
		var body map[string]any
		require.NoError(t, c.ShouldBindJSON(&body))
		require.Equal(t, "auto", body["model"])
		c.Status(204)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"auto","input":"Hi"}`)))
	require.Equal(t, 204, recorder.Code)
}

func TestTranslateResponsesNoPublicFallback(t *testing.T) {
	h := NewTranslateHandler(translate.NewAggregator(&translate.Config{}), nil)
	router := gin.New()
	router.POST("/v1/responses", h.TranslateModelMiddleware())
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"translate","input":"Hello"}`)))
	require.Equal(t, 502, recorder.Code)
	require.Contains(t, recorder.Body.String(), "translation_providers_failed")
}

func TestTranslateResponsesAcceptsCodexRequest(t *testing.T) {
	var translated []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		translated = append(translated, payload["q"])
		require.Equal(t, "zh", payload["target"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"translatedText":"你好"}`))
	}))
	defer upstream.Close()

	h := NewTranslateHandler(translate.NewAggregator(&translate.Config{LibreTranslate: &translate.LibreTranslateConfig{BaseURL: upstream.URL}}), nil)
	router := gin.New()
	router.POST("/v1/responses", h.TranslateModelMiddleware(), func(c *gin.Context) { t.Error("translate reached normal scheduler") })

	body := `{
		"model": "translate",
		"stream": true,
		"input": [
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"You are Codex"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>...</environment_context>"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Hello"}]}
		],
		"tools": [{"type":"function","name":"exec_command","parameters":{"type":"object"}}],
		"tool_choice": "auto",
		"parallel_tool_calls": true,
		"reasoning": {"effort":"none","summary":"auto"},
		"store": false,
		"include": ["reasoning.encrypted_content"],
		"prompt_cache_key": "thread-1",
		"client_metadata": {"thread_id":"thread-1"}
	}`
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body)))

	require.Equal(t, 200, recorder.Code)
	require.Equal(t, []string{"Hello"}, translated)
	require.Contains(t, recorder.Body.String(), "event: response.output_text.delta")
	require.Contains(t, recorder.Body.String(), "你好")
}
