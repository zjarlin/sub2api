package service

import (
	"encoding/json"
	"os"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

// Qoder 平台走 Qoder 官方 Model Server 的 OpenAI 兼容 Chat Completions 端点。
//
// 已确认的客户端请求契约（来自本机安装的 Qoder IDE 2026.921.1 与 Qoder CLI
// 1.0.9 bundle）：
//
//	POST https://api2-v2.qoder.sh/model/v1/chat/completions
//	Authorization: Bearer <Qoder access token>
//	Accept: text/event-stream
//	Content-Type: application/json
//	X-Request-ID / X-Session-ID: <uuid>
//
// body 为 OpenAI Chat Completions 格式，并固定 stream=true、
// stream_options.include_usage=true、附带 metadata.context 客户端身份。
// 生产 host 由客户端在 {prod,daily,test} 间选择，可用 QODER_MODEL_SERVER_HOST
// 覆盖，这里保持一致以复现真实流量。
const (
	// DefaultQoderModel 是管理员未指定模型时的提交消息生成模型。
	DefaultQoderModel = "auto"

	qoderModelServerProdHost = "api2-v2.qoder.sh"
)

// DefaultQoderModelIDs 返回 Qoder 提交消息可用模型候选。Qoder 客户端默认使用
// "auto"，由服务端按套餐路由到具体模型。
func DefaultQoderModelIDs() []string {
	return []string{DefaultQoderModel, "claude-sonnet-4-5", "claude-opus-4-5", "qwen3.8-max"}
}

func (a *Account) IsQoder() bool { return a != nil && a.Platform == PlatformQoder }

// QoderModelServerHost 解析 Model Server host，语义与官方客户端一致。
func QoderModelServerHost() string {
	host := strings.TrimSpace(os.Getenv("QODER_MODEL_SERVER_HOST"))
	if host == "" {
		return qoderModelServerProdHost
	}
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	return strings.TrimRight(host, "/")
}

// QoderChatCompletionsURL 是 Qoder 客户端真实使用的模型端点。
func QoderChatCompletionsURL() string {
	return "https://" + QoderModelServerHost() + "/model/v1/chat/completions"
}

// QoderModelServerURL 返回转发层需要的 base URL（不含 /v1/chat/completions）。
func QoderModelServerURL() string {
	return "https://" + QoderModelServerHost() + "/model/v1"
}

// QoderCommitMessageSystemPrompt 复现 Qoder 生成提交消息时的约定：
// Conventional Commits、单一主类型、只输出提交消息本身。
const QoderCommitMessageSystemPrompt = `You generate Git commit messages from a diff.
Follow the Conventional Commits format: type(scope): description.
Use one type among feat, fix, refactor, docs, test, chore, perf, build, ci, style, revert.
Pick the single most significant change as the type. Keep the subject under 72 characters.
Output only the commit message, with no quotes, notes, or explanations.`

// QoderCommitMessageSampleDiff 是账号管理探测用的最小真实 diff，保证请求体形状
// 与真实提交消息生成一致，同时不依赖调用方仓库状态。
const QoderCommitMessageSampleDiff = `diff --git a/internal/service/account.go b/internal/service/account.go
index 4f1c9a2..b7d3e10 100644
--- a/internal/service/account.go
+++ b/internal/service/account.go
@@ -120,6 +120,9 @@ type Account struct {
 	Priority int
+	// RateMultiplier scales usage accounting for this account.
+	RateMultiplier *float64
 	Status string
 }
diff --git a/internal/service/account_test_service.go b/internal/service/account_test_service.go
index 91ab004..2c7de55 100644
--- a/internal/service/account_test_service.go
+++ b/internal/service/account_test_service.go
@@ -440,6 +440,9 @@ func (s *AccountTestService) TestAccountConnection(c *gin.Context, accountID int64) error {
 	if account.IsOpenAI() {
 		return s.testOpenAIAccountConnection(c, account, modelID, prompt, normalizeAccountTestMode(mode))
 	}
+	if account.IsQoder() {
+		return s.testQoderCommitMessageConnection(c, account, modelID, prompt)
+	}
 	return s.testClaudeAccountConnection(c, account, modelID)
 }`

// QoderCommitMessageRequestBody 构造 Qoder 提交消息请求体。ids 允许测试注入
// 固定 uuid；传入空串时现场生成。
func QoderCommitMessageRequestBody(modelID, diff, requestID, sessionID, requestSetID, clientType string) map[string]any {
	if strings.TrimSpace(modelID) == "" {
		modelID = DefaultQoderModel
	}
	if strings.TrimSpace(diff) == "" {
		diff = QoderCommitMessageSampleDiff
	}
	if strings.TrimSpace(requestID) == "" {
		requestID = uuid.NewString()
	}
	if strings.TrimSpace(sessionID) == "" {
		sessionID = uuid.NewString()
	}
	if strings.TrimSpace(requestSetID) == "" {
		requestSetID = uuid.NewString()
	}
	if strings.TrimSpace(clientType) == "" {
		clientType = "qoder-ide"
	}

	return map[string]any{
		"model": modelID,
		"messages": []map[string]any{
			{"role": "system", "content": QoderCommitMessageSystemPrompt},
			{"role": "user", "content": "Generate a commit message for this diff:\n\n" + diff},
		},
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"metadata": map[string]any{
			"context": map[string]any{
				"request_id":        requestID,
				"request_set_id":    requestSetID,
				"session_id":        sessionID,
				"source_session_id": sessionID,
				"client_type":       clientType,
			},
		},
	}
}

// QoderCommitMessageHeaders 返回官方客户端的身份请求头（不含 Authorization）。
func QoderCommitMessageHeaders(requestID, sessionID string) map[string]string {
	if strings.TrimSpace(requestID) == "" {
		requestID = uuid.NewString()
	}
	if strings.TrimSpace(sessionID) == "" {
		sessionID = requestID
	}
	return map[string]string{
		"Accept":       "text/event-stream",
		"Content-Type": "application/json",
		"X-Request-ID": requestID,
		"X-Session-ID": sessionID,
	}
}

// QoderCommitMessageCurl 渲染管理员可视化用的 curl。令牌按真实请求写入，
// 但默认脱敏，避免把账号凭据回显到前端日志或审计记录里。
func QoderCommitMessageCurl(modelID, diff, token string) string {
	requestID := uuid.NewString()
	sessionID := uuid.NewString()
	payload := QoderCommitMessageRequestBody(modelID, diff, requestID, sessionID, uuid.NewString(), "qoder-ide")
	body, _ := json.MarshalIndent(payload, "", "  ")

	redacted := token
	if len(redacted) > 8 {
		redacted = redacted[:4] + "..." + redacted[len(redacted)-4:]
	} else if redacted != "" {
		redacted = "***"
	}

	lines := []string{
		"curl -N -X POST '" + QoderChatCompletionsURL() + "' \\",
		"  -H 'Authorization: Bearer " + redacted + "' \\",
	}
	for key, value := range QoderCommitMessageHeaders(requestID, sessionID) {
		lines = append(lines, "  -H '"+key+": "+value+"' \\")
	}
	lines = append(lines, "  -d '"+string(body)+"'")
	return strings.Join(lines, "\n")
}

// qoderAccountToken 读取账号保存的 Qoder 用户令牌。Qoder 账号把用户令牌存在
// api_key，兼容历史数据中的 access_token。
func qoderAccountToken(account *Account) string {
	if account == nil {
		return ""
	}
	if token := strings.TrimSpace(account.GetCredential("api_key")); token != "" {
		return token
	}
	return strings.TrimSpace(account.GetCredential("access_token"))
}

// validateQoderCredentials 校验 Qoder 账号：只支持 apikey，令牌直连官方
// Model Server，协议固定 chat_completions。
func validateQoderCredentials(platform, accountType string, credentials map[string]any) error {
	if platform != PlatformQoder {
		return nil
	}
	if accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest("INVALID_QODER_CREDENTIALS", "qoder requires an API key account holding a Qoder access token")
	}
	token, _ := credentials["api_key"].(string)
	if strings.TrimSpace(token) == "" {
		token, _ = credentials["access_token"].(string)
	}
	if strings.TrimSpace(token) == "" {
		return infraerrors.BadRequest("INVALID_QODER_CREDENTIALS", "qoder requires a Qoder access token")
	}
	protocol, _ := credentials["api_protocol"].(string)
	if protocol != "" && protocol != APIProtocolChatCompletions {
		return infraerrors.BadRequest("INVALID_QODER_CREDENTIALS", "qoder only supports the chat_completions upstream protocol")
	}
	return nil
}
