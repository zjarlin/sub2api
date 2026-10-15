package service

// OpenAIUpstreamDegenerationCode 是退化重复熔断对客户端可见的错误码。
//
// 它同时出现在两处，必须保持同一个来源：
//   - Responses 流的终止事件 response.failed.error.code
//   - 服务层返回、进而进 ops_error_logs 与账号健康统计的 error 文本前缀
//
// handler 侧据此判断终止事件是否已由 service 层写出（见
// openAIForwardErrorAlreadyCommunicated），所以该常量是对外契约，不可随意改名。
const OpenAIUpstreamDegenerationCode = "upstream_degenerate_repetition"

// openAIUpstreamDegenerationError 表示网关主动熔断了一次"上游退化重复"的流式响应。
//
// 之所以需要独立类型而不是继续用 fmt.Errorf：熔断反映的是账号所连上游链路的真实
// 故障（同一账号+模型卡在复读），必须能被账号健康熔断器识别并计数。用裸 error 时
// classifyOpenAIAPIKeyHealthFailure 只能返回"不计数"，退化熔断在账号维度上完全
// 不可见——账号不会被降权，同一账号+模型会反复复读，每次白烧约 50 秒。
//
// 这里刻意不实现 *UpstreamFailoverError：熔断发生在流中途，语义输出（至少终止事件
// 之前的增量）已经写给客户端，无法透明重放到下一个账号。把它伪装成可 failover 的
// 错误只会让 handler 走 handleFailoverExhausted，并用通用错误码覆盖掉客户端已经
// 看到的 upstream_degenerate_repetition。
type openAIUpstreamDegenerationError struct {
	message string
}

func newOpenAIUpstreamDegenerationError(message string) error {
	return &openAIUpstreamDegenerationError{message: message}
}

func (e *openAIUpstreamDegenerationError) Error() string {
	if e == nil {
		return OpenAIUpstreamDegenerationCode
	}
	return OpenAIUpstreamDegenerationCode + ": " + e.message
}
