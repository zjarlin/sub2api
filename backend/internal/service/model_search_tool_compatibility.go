package service

import "github.com/tidwall/gjson"

// 搜索声明本身可以重放，但 Chat 桥接会丢弃这些服务端工具，不能据此宣称候选兼容。
func modelRequestNeedsNativeSearchTools(body []byte) bool {
	needed, _ := modelRequestSearchToolKinds(body)
	return needed
}

func modelRequestSearchToolKinds(body []byte) (needed, preview bool) {
	needed, preview = modelSearchToolKinds(gjson.GetBytes(body, "tools"))
	gjson.GetBytes(body, "input").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "additional_tools" {
			more, morePreview := modelSearchToolKinds(item.Get("tools"))
			needed, preview = needed || more, preview || morePreview
		}
		return true
	})
	return needed, preview
}

func modelSearchToolKinds(tools gjson.Result) (needed, preview bool) {
	tools.ForEach(func(_, tool gjson.Result) bool {
		switch tool.Get("type").String() {
		case "web_search":
			needed = true
		case "web_search_preview", "web_search_preview_2025_03_11":
			needed, preview = true, true
		case "namespace":
			more, morePreview := modelSearchToolKinds(tool.Get("tools"))
			needed, preview = needed || more, preview || morePreview
		}
		return true
	})
	return needed, preview
}

func modelAccountPreservesSearchTools(account *Account, model string, body []byte) bool {
	needed, preview := modelRequestSearchToolKinds(body)
	if !needed {
		return true
	}
	if account == nil {
		return false
	}
	if account.Platform == PlatformGrok {
		return !preview && (account.Type == AccountTypeAPIKey || account.Type == AccountTypeOAuth)
	}
	if account.IsOpenCodeGo() {
		upstream := resolveOpenAIForwardModel(account, model, "")
		upstream = normalizeOpenAIModelForUpstream(account, upstream)
		return openCodeGoNativeProtocol(account, upstream) == APIProtocolResponses
	}
	if account.IsAnthropicProtocol() || shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
		return false
	}
	// 这里只保证原生工具声明不被转换丢弃；上游能力拒绝仍由 Auto 换候选处理。
	return account.IsOpenAI() || account.UsesNativeCNResponses()
}
