package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// openAIRegionRestrictedCode 是 OpenAI 兼容上游按调用来源国家/地区拦截请求时使用的
// 结构化错误码。
const openAIRegionRestrictedCode = "unsupported_country_region_territory"

// openAIRegionRestrictedMessage 是同一个错误的稳定文案（大小写不敏感匹配）。
const openAIRegionRestrictedMessage = "country, region, or territory not supported"

// isOpenAIRegionRestricted403 识别上游按调用来源国家/地区拦截的 403。
//
// OpenAI 兼容上游（含 opencode zen/go 与各中转）会回结构化 403：
//
//	{"error":{"code":"unsupported_country_region_territory",
//	          "message":"Country, region, or territory not supported",
//	          "param":null,"type":"request_forbidden"}}
//
// 这类错误描述的是「这条出口链路 / 这个地区被上游拒绝」，而不是账号凭据或权限
// 失效：同一个账号在别的出口、别的时刻仍可能成功——live 证据是该账号 856 同期
// deepseek 请求正常，且 gpt-6-luna 在 13:33 仍成功过。据此升级成账号级处罚会
// 让一次地区拦截把一个仍可服务其它模型的账号永久禁用（并发 403 在 failover
// 状态集里，还会被逐账号重放）。
//
// 与既有的 HTML 403（#5334）和 CN 供应商配额 403 同口径：只换号，不递增连续
// 403 计数、不设临时不可调度、不写账号 error。failover 行为不变——403 本就在
// failover 状态集里，换个走不同代理的账号仍有可能成功。
func isOpenAIRegionRestricted403(upstreamMsg string, responseBody []byte) bool {
	if isRegionRestrictedMessage(upstreamMsg) {
		return true
	}
	if len(responseBody) == 0 || !gjson.ValidBytes(responseBody) {
		return false
	}
	for _, path := range []string{"error.code", "response.error.code"} {
		if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(responseBody, path).String()), openAIRegionRestrictedCode) {
			return true
		}
	}
	for _, path := range []string{"error.message", "response.error.message"} {
		if isRegionRestrictedMessage(gjson.GetBytes(responseBody, path).String()) {
			return true
		}
	}
	return false
}

func isRegionRestrictedMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	return strings.Contains(message, openAIRegionRestrictedMessage) ||
		strings.Contains(message, openAIRegionRestrictedCode)
}
