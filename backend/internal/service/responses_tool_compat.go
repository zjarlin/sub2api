package service

import (
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// normalizeOpenAIResponsesWebSearchPreview adapts the newer Responses
// web_search declaration to upstreams that still expose its preview name.
func normalizeOpenAIResponsesWebSearchPreview(body []byte) ([]byte, bool) {
	paths := make([]string, 0, 2)
	for index, tool := range gjson.GetBytes(body, "tools").Array() {
		if tool.Get("type").String() == "web_search" {
			paths = append(paths, "tools."+strconv.Itoa(index)+".type")
		}
	}

	toolChoice := gjson.GetBytes(body, "tool_choice")
	if toolChoice.Type == gjson.String && toolChoice.String() == "web_search" {
		paths = append(paths, "tool_choice")
	} else if toolChoice.IsObject() && toolChoice.Get("type").String() == "web_search" {
		paths = append(paths, "tool_choice.type")
	}
	for index, tool := range toolChoice.Get("tools").Array() {
		if tool.Get("type").String() == "web_search" {
			paths = append(paths, "tool_choice.tools."+strconv.Itoa(index)+".type")
		}
	}

	normalized := body
	changed := false
	for _, path := range paths {
		next, err := sjson.SetBytes(normalized, path, "web_search_preview")
		if err != nil {
			continue
		}
		normalized = next
		changed = true
	}
	return normalized, changed
}
