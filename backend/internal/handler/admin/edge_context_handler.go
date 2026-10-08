package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type edgeContextField struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

type edgeAccountContext struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	BaseURL        string   `json:"base_url"`
	APIKeySet      bool     `json:"api_key_set"`
	APIKeyEditable bool     `json:"api_key_editable"`
	Status         string   `json:"status"`
	Schedulable    bool     `json:"schedulable"`
	Models         []string `json:"models"`
	GroupIDs       []int64  `json:"group_ids"`
}

type edgeServiceContext struct {
	Adapter    string               `json:"adapter"`
	Source     string               `json:"source"`
	Enabled    bool                 `json:"enabled"`
	Configured bool                 `json:"configured"`
	Error      string               `json:"error,omitempty"`
	Fields     []edgeContextField   `json:"fields"`
	Accounts   []edgeAccountContext `json:"accounts"`
}

// GetContexts 仅返回管理员可见的运行配置白名单，凭据始终保留在服务端。
func (h *VisionHandler) GetContexts(c *gin.Context) {
	if h.cfg == nil || h.accounts == nil {
		response.InternalError(c, "Edge configuration unavailable")
		return
	}
	contexts := map[string]*edgeServiceContext{}
	for key, adapter := range map[string]string{"vision": "YOLO / RapidOCR", "tts": "GPT-SoVITS", "dub": "Whisper / GPT-SoVITS", "generation": "Seedance / Ark", "laya": "Laya", "jev": "TypeSafe / JEV"} {
		contexts[key] = &edgeServiceContext{Adapter: adapter, Source: "deployment", Fields: []edgeContextField{}, Accounts: []edgeAccountContext{}}
	}
	vision := contexts["vision"]
	vision.Enabled = h.cfg.Gateway.Vision.Enabled
	vision.Configured = vision.Enabled
	vision.Fields = []edgeContextField{{"base_url", safeEdgeURL(h.cfg.Gateway.Vision.BaseURL())}, {"timeout_seconds", h.cfg.Gateway.Vision.TimeoutSeconds}, {"protocol", "Volcengine-compatible / multipart"}, {"authentication", "gateway"}}
	var health struct {
		ModelsLoaded bool `json:"models_loaded"`
	}
	if err := readEdgeRuntime(c.Request.Context(), h.cfg.Gateway.Vision.BaseURL(), "/health", &health); err != nil {
		vision.Error = "runtime_unavailable"
		vision.Enabled = false
	} else {
		vision.Fields = append(vision.Fields, edgeContextField{"models_loaded", health.ModelsLoaded})
	}
	var media struct {
		TTS struct {
			Enabled        bool   `json:"enabled"`
			URL            string `json:"upstream_url"`
			Timeout        int    `json:"timeout_seconds"`
			Language       string `json:"language"`
			Reference      string `json:"reference_audio"`
			Prompt         string `json:"prompt_text"`
			PromptLanguage string `json:"prompt_language"`
		} `json:"tts"`
		Dub struct {
			Enabled    bool   `json:"enabled"`
			URL        string `json:"upstream_url"`
			Timeout    int    `json:"timeout_seconds"`
			CommandSet bool   `json:"command_set"`
			MaxUpload  int64  `json:"max_upload_bytes"`
		} `json:"dub"`
	}
	mediaErr := readEdgeRuntime(c.Request.Context(), h.cfg.Gateway.Media.BaseURL(), "/internal/context", &media)
	for _, key := range []string{"tts", "dub"} {
		contexts[key].Fields = []edgeContextField{{"gateway_upstream", safeEdgeURL(h.cfg.Gateway.Media.BaseURL())}, {"authentication", "gateway"}}
		if mediaErr != nil {
			contexts[key].Error = "runtime_unavailable"
		}
	}
	tts := contexts["tts"]
	tts.Configured = media.TTS.URL != ""
	tts.Enabled = h.cfg.Gateway.Media.Enabled && media.TTS.Enabled && tts.Configured
	tts.Fields = append(tts.Fields, []edgeContextField{{"base_url", safeEdgeURL(media.TTS.URL)}, {"timeout_seconds", media.TTS.Timeout}, {"language", media.TTS.Language}, {"reference_audio", media.TTS.Reference}, {"prompt_text", media.TTS.Prompt}, {"prompt_language", media.TTS.PromptLanguage}}...)
	dub := contexts["dub"]
	dub.Configured = media.Dub.URL != "" || media.Dub.CommandSet
	dub.Enabled = h.cfg.Gateway.Media.Enabled && media.Dub.Enabled && dub.Configured
	dub.Fields = append(dub.Fields, []edgeContextField{{"base_url", safeEdgeURL(media.Dub.URL)}, {"timeout_seconds", media.Dub.Timeout}, {"command_set", media.Dub.CommandSet}, {"max_upload_bytes", media.Dub.MaxUpload}}...)
	for _, platform := range []string{service.PlatformSystemOne, service.PlatformOpenAI} {
		accounts, err := h.accounts.ListAccountsForSchedulerScoreFilter(c.Request.Context(), platform, "", "", "", 0, "", service.AccountListFilters{})
		if err != nil {
			response.InternalError(c, "Unable to load edge accounts")
			return
		}
		for i := range accounts {
			account := &accounts[i]
			key := edgeAccountService(account)
			if key == "" {
				continue
			}
			item := h.maskEdgeAccount(account)
			contexts[key].Accounts = append(contexts[key].Accounts, item)
			configured := item.BaseURL != "" && (key == "laya" || item.APIKeySet)
			contexts[key].Configured = contexts[key].Configured || configured
			contexts[key].Enabled = contexts[key].Enabled || (configured && account.IsSchedulable() && len(account.GroupIDs) > 0)
		}
	}
	for _, key := range []string{"generation", "laya", "jev"} {
		contexts[key].Source = "account"
	}
	response.Success(c, contexts)
}

func edgeAccountService(account *service.Account) string {
	if account.Platform == service.PlatformSystemOne {
		return account.SystemOneProvider()
	}
	if account.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilitySeedance) {
		return "generation"
	}
	return ""
}

func (h *VisionHandler) maskEdgeAccount(account *service.Account) edgeAccountContext {
	models := make([]string, 0, len(account.GetModelMapping()))
	for model := range account.GetModelMapping() {
		models = append(models, model)
	}
	sort.Strings(models)
	editable := true
	if account.IsLaya() {
		editable = h.cfg.BuiltinAdapter.LayaKey == ""
	}
	if account.IsJev() {
		editable = h.cfg.BuiltinAdapter.JevKey == ""
	}
	return edgeAccountContext{ID: account.ID, Name: account.Name, BaseURL: safeEdgeURL(account.GetOpenAIBaseURL()), APIKeySet: account.GetCredential("api_key") != "", APIKeyEditable: editable, Status: account.Status, Schedulable: account.IsSchedulable(), Models: models, GroupIDs: account.GroupIDs}
}

// UpdateContextAccount 复用账号更新校验和缓存失效，仅允许编辑该服务自己的地址与密钥。
func (h *VisionHandler) UpdateContextAccount(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	account, err := h.accounts.GetAccount(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	key := edgeAccountService(account)
	if key == "" || key != c.Param("service") {
		response.NotFound(c, "Account does not belong to this edge service")
		return
	}
	var req struct {
		BaseURL *string `json:"base_url"`
		APIKey  string  `json:"api_key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid context")
		return
	}
	credentials := map[string]any{}
	if strings.TrimSpace(req.APIKey) != "" {
		if !h.maskEdgeAccount(account).APIKeyEditable {
			response.BadRequest(c, "API key is managed by deployment configuration")
			return
		}
		credentials["api_key"] = strings.TrimSpace(req.APIKey)
	}
	if req.BaseURL != nil && key == "generation" {
		value := strings.TrimRight(strings.TrimSpace(*req.BaseURL), "/")
		u, err := url.Parse(value)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			response.BadRequest(c, "Invalid upstream URL")
			return
		}
		credentials["base_url"] = value
	}
	updated, err := h.accounts.UpdateAccount(c.Request.Context(), id, &service.UpdateAccountInput{Credentials: credentials})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.maskEdgeAccount(updated))
}

func safeEdgeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

// 目标只来自服务器部署配置，禁止重定向，避免浏览器输入改变内部探测地址。
func readEdgeRuntime(ctx context.Context, base, path string, target any) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("edge runtime unavailable")
	}
	return json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(target)
}
