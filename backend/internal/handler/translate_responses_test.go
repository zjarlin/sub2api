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
	req, err := parseTranslateResponseInput([]byte(`{"input":[{"role":"user","content":[{"type":"input_text","text":"你好"}]}],"metadata":{"target_language":"en"}}`))
	require.NoError(t, err)
	require.Equal(t, "en", req.TargetLang)
	require.Equal(t, []string{"你好"}, req.Text)
	for _, body := range []string{
		`{"input":""}`, `{"input":null}`, `{"input":42}`,
		`{"input":"Hi","tools":[{"type":"function"}]}`,
		`{"input":"Hi","previous_response_id":"resp_old"}`,
		`{"input":"Hi","store":true}`,
		`{"input":[{"role":"user","content":[{"type":"input_image"}]}]}`,
		`{"input":[{"role":"system","content":"Translate"}]}`,
	} {
		_, err := parseTranslateResponseInput([]byte(body))
		require.Error(t, err, body)
	}
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
