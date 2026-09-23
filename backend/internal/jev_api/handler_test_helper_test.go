package jev_api

import (
	"net/http/httptest"
	"strings"

	"github.com/gin-gonic/gin"
)

func newTestContext(rec *httptest.ResponseRecorder, body []byte) (*gin.Context, *gin.Engine) {
	gin.SetMode(gin.TestMode)
	c, engine := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/systemone", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, engine
}
