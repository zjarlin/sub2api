package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// MailAction 在管理端验证保存账号的真实收发，代理由服务端读取。
func (h *AccountHandler) MailAction(c *gin.Context) {
	if h.cfg == nil || !h.cfg.Gateway.Mail.Enabled {
		response.NotFound(c, "Mail service disabled")
		return
	}
	endpoint := c.Param("action")
	switch endpoint {
	case "folders", "messages", "message", "send":
	default:
		response.NotFound(c, "Unknown mail action")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var body map[string]any
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<20)
	if c.ShouldBindJSON(&body) != nil || body == nil {
		response.BadRequest(c, "Invalid mail request")
		return
	}
	if account.ProxyID != nil {
		account.Proxy, err = h.adminService.GetProxy(c.Request.Context(), *account.ProxyID)
		if err != nil {
			response.BadRequest(c, "Mail account proxy unavailable")
			return
		}
	}
	if err := service.MailAccountBody(account, body); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	target, err := url.Parse(h.cfg.Gateway.Mail.BaseURL())
	if err != nil || target.Scheme != "http" || target.Host == "" || target.User != nil {
		response.BadRequest(c, "Invalid mail upstream")
		return
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/" + endpoint
	data, err := json.Marshal(body)
	if err != nil {
		response.BadRequest(c, "Invalid mail request")
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target.String(), bytes.NewReader(data))
	if err != nil {
		response.BadRequest(c, "Invalid mail request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	timeout := time.Duration(h.cfg.Gateway.Mail.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	result, err := client.Do(req)
	if err != nil {
		response.Error(c, http.StatusBadGateway, "Mail service unavailable")
		return
	}
	defer result.Body.Close()
	data, err = io.ReadAll(io.LimitReader(result.Body, (32<<20)+1))
	if err != nil || len(data) > 32<<20 {
		response.Error(c, http.StatusBadGateway, "Mail response unavailable")
		return
	}
	c.Data(result.StatusCode, "application/json", data)
}
