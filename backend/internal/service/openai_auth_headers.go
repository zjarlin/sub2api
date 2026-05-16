package service

import "net/http"

var openAIUpstreamAuthHeaderNames = []string{
	"authorization",
	"x-api-key",
	"x-goog-api-key",
	"api-key",
}

func clearOpenAIUpstreamAuthHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	for _, name := range openAIUpstreamAuthHeaderNames {
		headers.Del(name)
	}
}

func applyOpenAIUpstreamAuthHeaders(headers http.Header, account *Account, token string) {
	if headers == nil {
		return
	}
	clearOpenAIUpstreamAuthHeaders(headers)
	if account != nil && account.IsOpenAIApiKey() {
		for name, values := range account.BuildOpenAIAuthHeaders(token) {
			for _, value := range values {
				headers.Add(name, value)
			}
		}
		return
	}
	headers.Set("authorization", "Bearer "+token)
}
