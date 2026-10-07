package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranslateHandlerUnknownProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewTranslateHandler(translate.NewAggregator(&translate.Config{}))
	r.POST("/translate", h.Translate)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/translate", strings.NewReader(`{"q":["Hello"],"target":"zh-CN","provider":"unknown"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}
}
