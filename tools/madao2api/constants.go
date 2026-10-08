// constants.go 码道（华为云 CodeArts 代码智能体，Web 端 /chat）上游常量。
//
// 上游是纯 Web 会话：鉴权靠华为云 SSO Cookie（关键 Cookie 为 cftk），
// 请求额外带 cftk 头与 x-codearts-doer-scenario-type: web。没有公开的
// OpenAI 兼容端点，因此适配器把码道的 Agent kernel 会话协议翻译为
// /v1/chat/completions。
package main

const (
	// defaultBaseURL 是码道 Web 端的站内根路径（单区域 cn-north-4）。
	defaultBaseURL = "https://devcloud.cn-north-4.huaweicloud.com/chat"
	// webLoginURL 是浏览器登录入口；登录完成后站内 Cookie 生效。
	webLoginURL = "https://devcloud.cn-north-4.huaweicloud.com/chat/login"
	// loginOrigin 是 SSO 登录域，Cookie 作用域挂在它下面。
	loginOrigin = "https://devcloud.cn-north-4.huaweicloud.com"

	// cftkCookieName 是码道 cftk 令牌的 Cookie 名（window['cftk_cookie_key_cf2']）。
	cftkCookieName = "devclouddevuibjtcftk"
	// defaultAgent 是 Agent kernel 默认工作模式（build / plan / ask）。
	defaultAgent = "build"
	// codebaseAgentID 是"码道 Work"内置专家 ID（AgentCenter 目录查询用）。
	codebaseAgentID = "a8bcb36232554267a5142361cc25a393"

	// 站内接口路径。
	epMe             = "/rest/me"
	epSSOUser        = "/snap-manager/v1/sso/user"
	epSessionCreate  = "/kernel/sessions/create"
	epSessionPrompt  = "/kernel/sessions/%s/prompt"
	epSessionEvents  = "/kernel/sessions/%s/events"
	epSessionClose   = "/kernel/sessions/%s/close"
	epModelsBuiltin  = "/PromptCenterService/v1/model/builtin"
	epAgentsDetail   = "/PromptCenterService/v1/agent-center/agents/detail"
	epPackageOverview = "/snap-manager/v1/package/overview"
	epBenefitClaim   = "/codebaseservice/v1/cloudagent/benefit/claim"

	// 站内请求统一头。
	scenarioHeaderKey   = "x-codearts-doer-scenario-type"
	scenarioHeaderValue = "web"
	langHeaderKey       = "X-Language"
	langHeaderValue     = "zh-cn"
	agentTypeHeaderKey  = "Agent-Type"
	agentTypeCodeBase   = "CodeBase"
)

// kernelModels 是 AgentCenter 目录不可用时回退的默认模型目录，
// 取自码道前端内置 KERNEL_MODELS（provider=inferhub-provider）。
var kernelModels = []builtinModel{
	{ID: "GLM-5.2", Label: "GLM-5.2", Description: "深度推理模型，适合复杂任务与深度思考"},
	{ID: "GLM-5.1", Label: "GLM-5.1", Description: "通用编码模型"},
	{ID: "Qwen3-VL-235B", Label: "Qwen3-VL-235B", Description: "多模态理解模型"},
	{ID: "maas-glm-4.7", Label: "GLM-4.7", Description: "轻量编码模型"},
}

// modelAliases 把常见 OpenAI 风格别名映射到码道模型 ID，
// 便于下游用 gpt-4o 之类的名字直连而不必改调用方。
var modelAliases = map[string]string{
	"madao":               "GLM-5.2",
	"madao-code":          "GLM-5.2",
	"glm-5.2":             "GLM-5.2",
	"glm-5.1":             "GLM-5.1",
	"qwen3-vl-235b":       "Qwen3-VL-235B",
	"maas-glm-4.7":        "maas-glm-4.7",
}
