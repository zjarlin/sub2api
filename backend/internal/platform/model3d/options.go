package model3d

import "strings"

// mergeOptions 把调用方传入的服务商原生参数合并进请求体。
//
// 空值（nil、空字符串、空数组）不覆盖已有默认值，避免客户端用空参数
// 把服务商默认行为意外关掉；显式的 false / 0 会被保留。
func mergeOptions(body map[string]any, options map[string]any) {
	for key, value := range options {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if isEmptyOption(value) {
			continue
		}
		body[key] = value
	}
}

func isEmptyOption(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case []string:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

// formatOrDefault 归一化期望格式，无法识别时返回服务商默认值。
func formatOrDefault(requested string, allowed map[string]string, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(requested))
	normalized = strings.TrimPrefix(normalized, ".")
	if normalized == "" {
		return fallback
	}
	if value, ok := allowed[normalized]; ok {
		return value
	}
	return fallback
}
