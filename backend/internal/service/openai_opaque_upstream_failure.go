package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// 部分中转把内部失败包装成 400，且抹掉具体原因。仅识别已知的通用错误封装，
// 允许同模型换账号，不猜测或删除请求字段，也不把该账号标记为失效。
func isOpenAIOpaqueUpstreamFailure(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest || !gjson.ValidBytes(body) {
		return false
	}
	err := gjson.GetBytes(body, "error")
	if err.Get("code").String() != "upstream_error" ||
		err.Get("type").String() != "upstream_error" ||
		strings.TrimSpace(err.Get("param").String()) != "" {
		return false
	}
	switch strings.TrimSpace(err.Get("message").String()) {
	case "请求未能完成，请检查请求参数、模型名称或输入内容后重试。", "Upstream request failed":
		return true
	default:
		return false
	}
}
