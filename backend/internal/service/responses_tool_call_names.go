package service

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var responsesToolIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type responsesToolIdentity struct {
	kind      string
	name      string
	namespace string
}

// normalizeResponsesToolCallNames 根据当前声明还原历史里的点号寻址名。
// 只改明确匹配的调用，不猜测未知工具，不改写参数、结果、调用 ID 或原始会话文件。
func normalizeResponsesToolCallNames(body []byte) ([]byte, error) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, nil
	}
	needsNormalization := false
	input.ForEach(func(_, item gjson.Result) bool {
		kind := item.Get("type").String()
		needsNormalization = (kind == "function_call" || kind == "custom_tool_call") && strings.Contains(item.Get("name").String(), ".")
		return !needsNormalization
	})
	// 正常请求不重建长历史，兼容处理只为实际出现的点号名称分配缓冲。
	if !needsNormalization {
		return body, nil
	}
	aliases := make(map[string]responsesToolIdentity)
	ambiguous := make(map[string]bool)
	add := func(alias string, identity responsesToolIdentity) {
		key := identity.kind + ":" + alias
		if previous, exists := aliases[key]; exists && previous != identity {
			ambiguous[key] = true
		}
		aliases[key] = identity
	}
	var collect func(gjson.Result, string)
	collect = func(tools gjson.Result, namespace string) {
		for _, tool := range tools.Array() {
			kind, name := tool.Get("type").String(), tool.Get("name").String()
			if !responsesToolIdentifier.MatchString(name) {
				continue
			}
			if kind == "namespace" && namespace == "" {
				children := tool.Get("tools")
				if !children.IsArray() {
					children = tool.Get("children")
				}
				collect(children, name)
				continue
			}
			if kind != "function" && kind != "custom" {
				continue
			}
			identity := responsesToolIdentity{kind: kind, name: name, namespace: namespace}
			if namespace == "" {
				// functions 是 Codex 给顶层工具展示的寻址前缀，不是 name 的一部分。
				add("functions."+name, identity)
			} else {
				add(namespace+"."+name, identity)
				add("functions."+namespace+"."+name, identity)
			}
		}
	}
	collect(gjson.GetBytes(body, "tools"), "")
	for _, item := range input.Array() {
		if item.Get("type").String() == "additional_tools" {
			collect(item.Get("tools"), "")
		}
	}
	if len(aliases) == 0 {
		return body, nil
	}

	var rebuilt bytes.Buffer
	rebuilt.WriteByte('[')
	changed := false
	for i, item := range input.Array() {
		if i > 0 {
			rebuilt.WriteByte(',')
		}
		kind := ""
		switch item.Get("type").String() {
		case "function_call":
			kind = "function"
		case "custom_tool_call":
			kind = "custom"
		}
		key := kind + ":" + item.Get("name").String()
		identity, found := aliases[key]
		namespace := item.Get("namespace").String()
		itemBody := []byte(item.Raw)
		if found && !ambiguous[key] && (namespace == "" || namespace == identity.namespace) {
			var err error
			itemBody, err = sjson.SetBytes(itemBody, "name", identity.name)
			if err != nil {
				return body, err
			}
			if identity.namespace != "" {
				itemBody, err = sjson.SetBytes(itemBody, "namespace", identity.namespace)
				if err != nil {
					return body, err
				}
			}
			changed = true
		}
		rebuilt.Write(itemBody)
	}
	rebuilt.WriteByte(']')
	if !changed {
		return body, nil
	}
	return sjson.SetRawBytes(body, "input", rebuilt.Bytes())
}
