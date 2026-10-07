package service

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 免密/公开池账号在出站前统一改写鉴权与指纹头。
//   - OpenCode Free：Bearer public + desktop 指纹头（缺失会被 403 FreeTierError）
//   - Kilo：完全无鉴权，删除 Authorization/x-api-key
//
// 只在账号命中免费模式时生效，付费 Zen/Go 与普通上游不受影响。
func applyFreeLaneRequestHeaders(account *Account, headers http.Header, sessionSeed string) {
	if account == nil || headers == nil {
		return
	}
	switch {
	case account.IsOpenCodeFree():
		headers.Set("Authorization", "Bearer public")
		headers.Del("x-api-key")
		if strings.TrimSpace(headers.Get("User-Agent")) == "" {
			headers.Set("User-Agent", openCodeFreeUserAgent)
		}
		headers.Set(openCodeClientHeader, openCodeClientName)
		headers.Set(openCodeProjectHeader, openCodeProjectName)
		if session := kiloSessionForConversation(sessionSeed); session != "" {
			headers.Set(openCodeSessionHeader, session)
		}
		if headers.Get(openCodeRequestHeader) == "" {
			headers.Set(openCodeRequestHeader, kiloRequestID())
		}
	case account.IsKilo():
		headers.Del("Authorization")
		headers.Del("x-api-key")
		if strings.TrimSpace(headers.Get("User-Agent")) == "" {
			headers.Set("User-Agent", kiloUserAgent)
		}
	}
}

const (
	openCodeFreeUserAgent = "opencode/1.18.31"
	kiloUserAgent         = "sub2api-kilo"
)

// normalizeFreeLaneCredentials 为免密账号补齐非空占位凭据，避免账号表与
// 通用凭证校验把"无 key"误判为配置缺失。出站时由 applyFreeLaneRequestHeaders
// 翻译成各网关要求的真实鉴权形态。
func normalizeFreeLaneCredentials(platform string, credentials map[string]any) {
	if credentials == nil {
		return
	}
	switch platform {
	case PlatformKilo:
		normalizeKiloCredentials(credentials)
	case PlatformOpenCodeGo:
		mode, _ := credentials["account_mode"].(string)
		if strings.TrimSpace(mode) != AccountModeFree {
			return
		}
		if value, _ := credentials["api_key"].(string); strings.TrimSpace(value) == "" {
			credentials["api_key"] = "public"
		}
		if value, _ := credentials["api_protocol"].(string); strings.TrimSpace(value) == "" {
			credentials["api_protocol"] = APIProtocolAdaptive
		}
	}
}

// openCodeFreeFingerprintTools 是免费池工具指纹闸门要求的四个小写工具名。
// 缺任意一个都会返回 403 FreeTierError。已声明的同义工具（大小写变体）先做
// 规范化去重，缺口再补占位工具。
var openCodeFreeFingerprintTools = []string{"bash", "glob", "grep", "read"}

// freeLaneToolStyle 描述目标上游的 tools 结构：
//   - chat：Chat Completions，{type:function,function:{name,...}}
//   - responses：Responses，{type:function,name,...}
//   - anthropic：Messages，{name,description,input_schema}
type freeLaneToolStyle int

const (
	freeLaneToolStyleChat freeLaneToolStyle = iota
	freeLaneToolStyleResponses
	freeLaneToolStyleAnthropic
)

// applyFreeLaneBodyFingerprint 只对 OpenCode 免费模式改写请求体：
// 满足 body.tools 的工具四件套指纹要求。Kilo 与付费模式原样返回。
func applyFreeLaneBodyFingerprint(account *Account, body []byte, style freeLaneToolStyle) []byte {
	if account == nil || !account.IsOpenCodeFree() || len(body) == 0 || !json.Valid(body) {
		return body
	}
	tools := gjson.GetBytes(body, "tools")
	existing := make(map[string]struct{})
	out := make([]map[string]any, 0, 4)
	if tools.IsArray() {
		for _, tool := range tools.Array() {
			name := strings.ToLower(strings.TrimSpace(tool.Get("function.name").String()))
			if name == "" {
				name = strings.ToLower(strings.TrimSpace(tool.Get("name").String()))
			}
			if name != "" {
				if _, dup := existing[name]; dup {
					continue
				}
				existing[name] = struct{}{}
			}
			var decoded map[string]any
			if err := json.Unmarshal([]byte(tool.Raw), &decoded); err == nil {
				out = append(out, decoded)
			}
		}
	}
	for _, name := range openCodeFreeFingerprintTools {
		if _, ok := existing[name]; ok {
			continue
		}
		out = append(out, freeLaneFingerprintTool(name, style))
	}
	updated, err := sjson.SetBytes(body, "tools", out)
	if err != nil {
		return body
	}
	if !gjson.GetBytes(updated, "tool_choice").Exists() {
		if choice, err := sjson.SetBytes(updated, "tool_choice", "auto"); err == nil {
			updated = choice
		}
	}
	return updated
}

func freeLaneFingerprintTool(name string, style freeLaneToolStyle) map[string]any {
	const description = "This tool is currently unavailable and must not be used."
	switch style {
	case freeLaneToolStyleResponses:
		return map[string]any{
			"type":        "function",
			"name":        name,
			"description": description,
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
		}
	case freeLaneToolStyleAnthropic:
		return map[string]any{
			"name":         name,
			"description":  description,
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{}},
		}
	default:
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": description,
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		}
	}
}

// resolveFreeLaneSessionSeed 选择稳定的会话种子，让同一对话复用一个上游 session。
// 优先级：调用方会话头 -> 请求体 prompt_cache_key / metadata.user_id -> 全局兜底。
func resolveFreeLaneSessionSeed(c *gin.Context, body []byte) string {
	if c != nil && c.Request != nil {
		for _, header := range []string{openCodeSessionHeader, "session_id", "conversation_id", "X-Claude-Code-Session-Id"} {
			if value := strings.TrimSpace(c.GetHeader(header)); value != "" {
				return value
			}
		}
	}
	if len(body) > 0 {
		if value := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); value != "" {
			return value
		}
		if value := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String()); value != "" {
			return value
		}
	}
	return "global"
}
