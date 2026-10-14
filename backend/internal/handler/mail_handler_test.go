package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

func TestMailProxyDisabledReturnsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &GatewayHandler{}
	router.Any("/v1/mail/*proxyPath", h.MailProxy)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/mail/messages", strings.NewReader(`{}`))
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled mail proxy status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

// 未鉴权时必须先被 API Key 校验拦住，不能把请求透传到上游。
func TestMailProxyRequiresAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &GatewayHandler{cfg: &config.Config{}}
	h.cfg.Gateway.Mail.Enabled = true
	router.Any("/v1/mail/*proxyPath", h.MailProxy)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/mail/messages", strings.NewReader(`{}`))
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated mail proxy status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

// 未在白名单内的路径必须 404，避免内部/调试端点被暴露。
func TestMailProxyRejectsUnknownEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &GatewayHandler{cfg: &config.Config{}}
	h.cfg.Gateway.Mail.Enabled = true
	router.Any("/v1/mail/*proxyPath", h.MailProxy)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/mail/internal/context", strings.NewReader(`{}`))
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown mail endpoint status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

// 收件端点只接受 POST；GET 应返回 405 而不是透传。
func TestMailProxyEnforcesMethod(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &GatewayHandler{cfg: &config.Config{}}
	h.cfg.Gateway.Mail.Enabled = true
	router.Any("/v1/mail/*proxyPath", h.MailProxy)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/mail/messages", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET mail messages status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}
