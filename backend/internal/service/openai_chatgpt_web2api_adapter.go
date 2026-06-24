package service

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const chatGPTWeb2APIDefaultBaseURL = "http://127.0.0.1:8080/v1"

var chatGPTWeb2APIModelMap = map[string]string{
	"auto":             "auto",
	"gpt-5.5":          "gpt-5-5",
	"gpt-5.5-thinking": "gpt-5-5-thinking",
	"gpt-5.3":          "gpt-5-3",
	"gpt-5.2":          "gpt-5-2",
	"gpt-5.1":          "gpt-5-1",
	"gpt-5":            "gpt-5",
	"gpt-5-mini":       "gpt-5-mini",
	"gpt-5.3-mini":     "gpt-5-3-mini",
	"gpt-4o":           "auto",
	"gpt-4":            "gpt-5",
	"gpt-3.5-turbo":    "gpt-5-mini",
	"chatgpt":          "auto",
	"chatgpt-web2api":  "auto",
	"gpt-5-5":          "gpt-5-5",
	"gpt-5-5-thinking": "gpt-5-5-thinking",
	"gpt-5-3":          "gpt-5-3",
	"gpt-5-2":          "gpt-5-2",
	"gpt-5-1":          "gpt-5-1",
	"gpt-5-3-mini":     "gpt-5-3-mini",
}

type chatGPTWeb2APIHealthStatus struct {
	Status         string
	CDPConnected   bool
	RequestsServed int64
}

func accountUsesChatGPTWeb2API(account *Account) bool {
	if account == nil || !account.IsOpenAIApiKey() {
		return false
	}
	if strings.EqualFold(account.GetOpenAIVendor(), "chatgpt-web2api") {
		return true
	}
	return openAIBaseURLLooksLikeChatGPTWeb2API(strings.ToLower(strings.TrimSpace(account.GetOpenAIBaseURL())))
}

func normalizeChatGPTWeb2APIModel(model string) string {
	trimmed := normalizeOpenAICompatibleUpstreamModel(model)
	if trimmed == "" {
		return "auto"
	}
	lookup := strings.ToLower(trimmed)
	if mapped, ok := chatGPTWeb2APIModelMap[lookup]; ok {
		return mapped
	}
	return trimmed
}

func buildChatGPTWeb2APIHealthURL(base string) (string, error) {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		trimmed = chatGPTWeb2APIDefaultBaseURL
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid ChatGPT-Web2API base URL: %s", base)
	}
	parsed.Path = strings.TrimRight(trimChatGPTWeb2APIBasePath(parsed.Path), "/") + "/health"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func trimChatGPTWeb2APIBasePath(pathValue string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(pathValue), "/")
	if trimmed == "" {
		return ""
	}
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions", "/v1", "/health"} {
		if strings.HasSuffix(strings.ToLower(trimmed), suffix) {
			return trimmed[:len(trimmed)-len(suffix)]
		}
	}
	return trimmed
}

func parseChatGPTWeb2APIHealth(body []byte) (chatGPTWeb2APIHealthStatus, error) {
	var raw struct {
		Status         string `json:"status"`
		CDPConnected   bool   `json:"cdp_connected"`
		RequestsServed int64  `json:"requests_served"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return chatGPTWeb2APIHealthStatus{}, err
	}
	return chatGPTWeb2APIHealthStatus{
		Status:         strings.ToLower(strings.TrimSpace(raw.Status)),
		CDPConnected:   raw.CDPConnected,
		RequestsServed: raw.RequestsServed,
	}, nil
}

func chatGPTWeb2APIHealthErrorMessage(status chatGPTWeb2APIHealthStatus) string {
	if status.Status == "" {
		return "ChatGPT-Web2API /health returned an empty status"
	}
	if status.Status != "ok" {
		if !status.CDPConnected {
			return fmt.Sprintf("ChatGPT-Web2API is %s: Chrome CDP is not connected; start Chrome with remote debugging and log in to ChatGPT", status.Status)
		}
		return fmt.Sprintf("ChatGPT-Web2API is %s", status.Status)
	}
	if !status.CDPConnected {
		return "ChatGPT-Web2API /health is ok but Chrome CDP is not connected"
	}
	return ""
}
