package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// v0ChatHandle 将会话绑定到调用者与上游账号，避免共享账号泄露其他用户的项目。
type v0ChatHandle struct {
	Chat       string `json:"chat"`
	Account    int64  `json:"account"`
	Key        int64  `json:"key"`
	Group      int64  `json:"group"`
	Expires    int64  `json:"expires"`
	Credential string `json:"credential"`
}

func signV0Handle(handle v0ChatHandle, secret string) string {
	data, _ := json.Marshal(handle)
	payload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte("sub2api-v0:"+secret))
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func parseV0Handle(token, secret string) (v0ChatHandle, bool) {
	var handle v0ChatHandle
	parts := strings.Split(token, ".")
	if len(parts) != 2 || secret == "" {
		return handle, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return handle, false
	}
	mac := hmac.New(sha256.New, []byte("sub2api-v0:"+secret))
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return handle, false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(data, &handle) != nil {
		return handle, false
	}
	return handle, handle.Chat != "" && handle.Account > 0 && handle.Expires > time.Now().Unix()
}

// v0Endpoint 仅开放生成、追问与产物读取，不公开团队会话目录或管理接口。
func v0Endpoint(method, path string) (token, suffix string, ok bool) {
	if method == http.MethodPost && path == "chats" {
		return "", "", true
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] != "chats" {
		return "", "", false
	}
	suffix = strings.Join(parts[2:], "/")
	allowed := method == http.MethodGet && (suffix == "" || suffix == "files" || suffix == "files/download" || suffix == "preview" || suffix == "messages")
	allowed = allowed || method == http.MethodPost && suffix == "messages"
	return parts[1], suffix, allowed
}

// V0Proxy 用网关签发的句柄访问 v0，会话始终回到创建它的账号。
func (h *GatewayHandler) V0Proxy(c *gin.Context) {
	token, suffix, allowed := v0Endpoint(c.Request.Method, strings.Trim(c.Param("proxyPath"), "/"))
	if !allowed {
		c.JSON(404, gin.H{"error": "Unsupported v0 endpoint"})
		return
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.GroupID == nil || key.Group == nil || key.User == nil {
		c.JSON(401, gin.H{"error": "API key with group required"})
		return
	}
	if key.Group.Platform != service.PlatformV0 && key.Group.Platform != service.PlatformComposite {
		c.JSON(403, gin.H{"error": "v0 or composite group required"})
		return
	}
	if h.cfg == nil || h.cfg.JWT.Secret == "" {
		c.JSON(503, gin.H{"error": "v0 signing unavailable"})
		return
	}
	var handle v0ChatHandle
	if token != "" {
		handle, ok = parseV0Handle(token, h.cfg.JWT.Secret)
		if !ok || handle.Key != key.ID || handle.Group != *key.GroupID {
			c.JSON(404, gin.H{"error": "v0 chat not found or expired"})
			return
		}
	}
	subscription, _ := middleware.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), key.User, key, key.Group, subscription, service.PlatformV0); err != nil {
		status, code, message, _ := billingErrorDetails(err)
		c.JSON(status, gin.H{"error": gin.H{"type": code, "message": message}})
		return
	}
	ctx := context.WithValue(c.Request.Context(), ctxkey.ForcePlatform, service.PlatformV0)
	selection, err := h.gatewayService.SelectV0Account(ctx, key.GroupID, handle.Account, key.UserID)
	if err != nil || selection == nil || selection.Account == nil {
		c.JSON(503, gin.H{"error": "No available v0 account"})
		return
	}
	if selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	if !selection.Acquired {
		c.JSON(429, gin.H{"error": "v0 account busy"})
		return
	}
	account := selection.Account
	credential := service.V0AccountAPIKey(account)
	fingerprint := sha256.Sum256([]byte(credential))
	identity := base64.RawURLEncoding.EncodeToString(fingerprint[:])
	if credential == "" || token != "" && handle.Credential != identity {
		c.JSON(503, gin.H{"error": "v0 account credential changed"})
		return
	}
	target, err := url.Parse(service.V0AccountBaseURL(account))
	if err != nil || target.Scheme != "https" || target.Hostname() != "api.v0.dev" || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		c.JSON(503, gin.H{"error": "Invalid v0 upstream"})
		return
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/chats"
	if token != "" {
		target.Path += "/" + handle.Chat
		if suffix != "" {
			target.Path += "/" + suffix
		}
	}
	target.RawQuery = c.Request.URL.RawQuery
	upstreamCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(upstreamCtx, c.Request.Method, target.String(), c.Request.Body)
	if err != nil {
		c.JSON(502, gin.H{"error": "Invalid upstream request"})
		return
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(502, gin.H{"error": "v0 upstream unavailable"})
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
	if err != nil || len(data) > 64<<20 {
		c.JSON(502, gin.H{"error": "v0 response exceeds limit or is incomplete"})
		return
	}
	if token == "" && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var chat struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &chat) != nil || chat.ID == "" || strings.ContainsAny(chat.ID, "/?#\\") {
			c.JSON(502, gin.H{"error": "v0 returned invalid chat"})
			return
		}
		handle = v0ChatHandle{Chat: chat.ID, Account: account.ID, Key: key.ID, Group: *key.GroupID, Expires: time.Now().Add(365 * 24 * time.Hour).Unix(), Credential: identity}
		token = signV0Handle(handle, h.cfg.JWT.Secret)
	}
	// v0 未配置统一调用单价，先记录账号关联，不虚构 token 或额外扣费。
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && c.Request.Method == http.MethodPost {
		err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
			Result: &service.ForwardResult{RequestID: "v0:" + uuid.NewString(), Model: "v0-app"},
			APIKey: key, User: key.User, Account: account, Subscription: subscription,
			InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: target.Path,
			UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c),
			APIKeyService: h.apiKeyService, QuotaPlatform: service.PlatformV0,
		})
		if err != nil {
			// 上游已生成项目，保留返回值与句柄，避免客户端重试产生重复消费。
			logger.L().With(zap.String("component", "handler.v0")).Error("usage_record_failed", zap.Error(err))
			c.Header("X-V0-Usage-Status", "record-failed")
		}
	}
	c.Header("Cache-Control", "no-store")
	if token != "" {
		c.Header("X-V0-Chat-Handle", token)
	}
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), data)
}
