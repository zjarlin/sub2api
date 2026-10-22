package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/qoder"
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

// QoderBuiltinCatalogWarningCode 表示本次同步没有读到签名目录，使用了内置官方模型表。
const QoderBuiltinCatalogWarningCode = "qoder_catalog_builtin"

// QoderCatalogSyncedWarningCode 表示本次同步读到了官方签名目录（真实套餐目录）。
const QoderCatalogSyncedWarningCode = "qoder_catalog_synced"

// QoderSignedCatalogPath 是官方客户端拉取模型目录的路径。
//
// 注意 /algo 前缀：官方签名器（qoder_auth WASM 的 qodercontext_prepareRequest）
// 会把 /api/v2/... 改写为 /algo/api/v2/...；直接请求不带前缀的路径固定 404。
const QoderSignedCatalogPath = "/api/v2/model/list?Encode=1"

// QoderCatalogScene 是目录里决定「文本对话可用模型」的场景名。
const QoderCatalogScene = "chat"

// QoderBuiltinModel 是内置官方目录的一项。
//
// 来自 Qoder CLI 1.1.49 的 chat 场景实测目录（2026-10-21）。
// Free 档位只有 qfmodel / dfmodel 的 enable=true，其余模型需要付费套餐；
// 内置表只用于「拿不到签名目录时」的候选与展示名回退，不代表账号已授权。
type QoderBuiltinModel struct {
	ID              string
	DisplayName     string
	ContextWindow   int64
	MaxOutputTokens int64
	Reasoning       bool
	Vision          bool
	Free            bool
	Default         bool
}

// QoderBuiltinModels 是 Qoder 官方 chat 场景的完整内置目录，顺序与上游一致。
func QoderBuiltinModels() []QoderBuiltinModel {
	return []QoderBuiltinModel{
		{ID: "auto", DisplayName: "Auto", ContextWindow: 200000, Vision: true},
		{ID: "ultimate", DisplayName: "Ultimate", ContextWindow: 1000000, Reasoning: true, Vision: true},
		{ID: "efficient", DisplayName: "Efficient", ContextWindow: 200000, Vision: true},
		{ID: "smodel", DisplayName: "Sonus", ContextWindow: 180000, Reasoning: true, Vision: true},
		{ID: "cmodel", DisplayName: "Cantus", ContextWindow: 180000, Reasoning: true, Vision: true},
		{ID: "qmodel_38max", DisplayName: "Qwen3.8-Max", ContextWindow: 180000, Reasoning: true, Vision: true},
		{ID: "qfmodel", DisplayName: "Qwen3.8-Flash", ContextWindow: 180000, Reasoning: true, Vision: true, Free: true, Default: true},
		{ID: "qmodel", DisplayName: "Qwen3.7-Plus", ContextWindow: 1000000, Vision: true},
		{ID: "kmodel_latest", DisplayName: "Kimi-K3", ContextWindow: 180000, Vision: true},
		{ID: "kmodel", DisplayName: "Kimi-K2.8-Preview", Vision: true},
		{ID: "gmodel", DisplayName: "GLM-5.3", ContextWindow: 180000, Reasoning: true, Vision: true},
		{ID: "gfmodel", DisplayName: "GLM-5.3-Flash", ContextWindow: 1000000, Reasoning: true, Vision: true},
		{ID: "dmodel", DisplayName: "DeepSeek-V4-Pro", ContextWindow: 1000000, Reasoning: true, Vision: true},
		{ID: "dfmodel", DisplayName: "DeepSeek-Flash", ContextWindow: 1000000, Reasoning: true, Vision: true, Free: true},
		{ID: "mmodel", DisplayName: "MiniMax-M3.1-Flash", ContextWindow: 180000, Vision: true},
	}
}

// DefaultQoderModelIDs 是 chat 场景的内置模型键，顺序与上游目录一致。
//
// 该表用于选择器候选、映射补全与「同步失败时的回退」；账号真实可用性以
// 签名目录（QoderCatalogEntry.Enable）或 Qoder 套餐为准。
func DefaultQoderModelIDs() []string {
	builtins := QoderBuiltinModels()
	ids := make([]string, 0, len(builtins))
	for _, model := range builtins {
		ids = append(ids, model.ID)
	}
	return ids
}

// QoderBuiltinModelMetadata 把内置目录转换为能力元数据，供目录回退时补全展示名。
func QoderBuiltinModelMetadata(model QoderBuiltinModel) UpstreamModelMetadata {
	metadata := UpstreamModelMetadata{
		ID:               model.ID,
		DisplayName:      model.DisplayName,
		ContextWindow:    model.ContextWindow,
		MaxContextWindow: model.ContextWindow,
		MaxOutputTokens:  model.MaxOutputTokens,
	}
	if model.Reasoning {
		metadata.Reasoning = qoderBoolPtr(true)
	}
	if model.Vision {
		metadata.InputModalities = []string{"text", "image"}
	} else {
		metadata.InputModalities = []string{"text"}
	}
	return metadata
}

func qoderBoolPtr(value bool) *bool { return &value }

// QoderCatalogEntry 是签名目录里 chat 场景的一个模型。
//
// Enable 表示「当前账号套餐是否可直接调度该模型」；Free 表示该模型对免费
// 档位开放。二者都来自上游，而不是本地推断。
type QoderCatalogEntry struct {
	Key             string
	DisplayName     string
	Enable          bool
	Free            bool
	Default         bool
	Reasoning       bool
	Vision          bool
	ContextWindow   int64
	ContextWindows  []int64
	MaxOutputTokens int64
	PriceFactor     float64
	Efforts         []string
	DefaultEffort   string
}

// QoderCatalog 是一次签名目录拉取的结果。
type QoderCatalog struct {
	Source string
	Scenes map[string][]QoderCatalogEntry
}

// ChatModels 返回 chat 场景条目。
func (c *QoderCatalog) ChatModels() []QoderCatalogEntry {
	if c == nil {
		return nil
	}
	return c.Scenes[QoderCatalogScene]
}

// QoderBuiltinModelCatalog 构造 Qoder 的内置目录：模型 ID 来自官方客户端键，
// 展示名优先复用账号已保存的元数据快照，避免把内置候选当作上游授权结果。
func QoderBuiltinModelCatalog(account *Account) *UpstreamModelCatalog {
	ids := QoderAccountModelIDs(account)
	catalog := &UpstreamModelCatalog{
		Models:   ids,
		Metadata: make(map[string]UpstreamModelMetadata, len(ids)),
	}
	catalog.Warnings = append(catalog.Warnings, UpstreamModelSyncWarning{
		Code: QoderBuiltinCatalogWarningCode,
		Message: "Qoder model catalog could not be fetched with the signed client request; " +
			"this sync returned the built-in official chat catalog. Account entitlement still depends on the Qoder plan.",
	})
	for _, modelID := range ids {
		if metadata, ok := account.GetUpstreamModelMetadata(modelID); ok && metadata.DisplayName != "" {
			catalog.Metadata[modelID] = metadata
			continue
		}
		if builtin, ok := qoderBuiltinModelByID(modelID); ok {
			catalog.Metadata[modelID] = QoderBuiltinModelMetadata(builtin)
		}
	}
	return catalog
}

// QoderAccountModelIDs 返回账号可用的 chat 模型键。
//
// 已同步签名目录时只返回 enable=true 的模型（当前可调度）；未同步或旧快照
// 缺少可用性字段时回退内置 chat 目录。
func QoderAccountModelIDs(account *Account) []string {
	if ids := qoderEnabledSnapshotModelIDs(account); len(ids) > 0 {
		return ids
	}
	return DefaultQoderModelIDs()
}

// qoderSnapshotModelIDs 返回账号快照里全部 chat 模型键（含 enable=false）。
func qoderSnapshotModelIDs(account *Account) []string {
	if account == nil {
		return nil
	}
	snapshot := account.GetUpstreamSupportedModelsSnapshot()
	if snapshot == nil || len(snapshot.Models) == 0 {
		return nil
	}
	ids := make([]string, 0, len(snapshot.Models))
	seen := make(map[string]bool, len(snapshot.Models))
	for _, id := range snapshot.Models {
		id = strings.TrimSpace(id)
		if id == "" || strings.Contains(id, "*") || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func qoderEnabledSnapshotModelIDs(account *Account) []string {
	ids := qoderSnapshotModelIDs(account)
	if len(ids) == 0 {
		return nil
	}
	enabled := make([]string, 0, len(ids))
	withAvailability := false
	for _, id := range ids {
		metadata, ok := account.GetUpstreamModelMetadata(id)
		if !ok || metadata.Enable == nil {
			enabled = append(enabled, id)
			continue
		}
		withAvailability = true
		if *metadata.Enable {
			enabled = append(enabled, id)
		}
	}
	// 旧快照没有 enable 字段时保持原样，不能因此把所有模型都隐藏掉。
	if !withAvailability || len(enabled) == 0 {
		return ids
	}
	return enabled
}

// QoderAccountModel 是账号模型列表的 Qoder 专用条目。
//
// 复用并且只保留 openai.Model 的公开字段，再追加可用性字段；不改 openai.Model
// 本身，避免影响网关 /v1/models 的共享契约。
type QoderAccountModel struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	// Enable/Free/Default 为 nil 表示该快照未记录可用性（旧快照或内置回退）。
	Enable  *bool `json:"enable,omitempty"`
	Free    *bool `json:"free,omitempty"`
	Default *bool `json:"default,omitempty"`
}

// QoderAccountModels 返回测试/列表候选：已同步目录时按目录顺序给出全部 chat
// 模型（含未启用的，便于管理员看到套餐差异），未同步时回退内置目录。
//
// 快照带可用性字段时额外透出 enable/free/default：前端据此标注「当前套餐不可用」，
// 并默认选中免费且启用的模型，避免测试弹窗默认落在会直接 402 的模型上。
func QoderAccountModels(account *Account) []QoderAccountModel {
	entries := qoderAccountCatalogEntries(account)
	if len(entries) == 0 {
		ids := DefaultQoderModelIDs()
		entries = make([]QoderCatalogEntry, 0, len(ids))
		for _, id := range ids {
			entry := QoderCatalogEntry{Key: id, Enable: true}
			if builtin, ok := qoderBuiltinModelByID(id); ok {
				entry.DisplayName = builtin.DisplayName
				entry.Free = builtin.Free
				entry.Default = builtin.Default
			}
			entries = append(entries, entry)
		}
	}
	models := make([]QoderAccountModel, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(entry.Key)
		if id == "" || strings.Contains(id, "*") || seen[id] {
			continue
		}
		seen[id] = true
		name := strings.TrimSpace(entry.DisplayName)
		if name == "" {
			name = id
		}
		model := QoderAccountModel{ID: id, Object: "model", Type: "model", OwnedBy: PlatformQoder, DisplayName: name}
		if metadata, ok := account.GetUpstreamModelMetadata(id); ok {
			model.Enable = metadata.Enable
			model.Free = metadata.Free
			model.Default = metadata.Default
		}
		models = append(models, model)
	}
	return models
}


func qoderBuiltinModelByID(id string) (QoderBuiltinModel, bool) {
	for _, model := range QoderBuiltinModels() {
		if model.ID == id {
			return model, true
		}
	}
	return QoderBuiltinModel{}, false
}

// QoderDefaultTestModel 返回测试弹窗的默认模型：优先目录里的默认模型，
// 其次免费且启用的模型，最后回退第一个。
func QoderDefaultTestModel(account *Account) string {
	entries := qoderAccountCatalogEntries(account)
	for _, entry := range entries {
		if entry.Default && entry.Enable {
			return entry.Key
		}
	}
	for _, entry := range entries {
		if entry.Free && entry.Enable {
			return entry.Key
		}
	}
	ids := QoderAccountModelIDs(account)
	if len(ids) > 0 {
		return ids[0]
	}
	return DefaultQoderModel
}

func qoderAccountCatalogEntries(account *Account) []QoderCatalogEntry {
	ids := qoderSnapshotModelIDs(account)
	entries := make([]QoderCatalogEntry, 0, len(ids))
	for _, id := range ids {
		entry := QoderCatalogEntry{Key: id, Enable: true}
		if metadata, ok := account.GetUpstreamModelMetadata(id); ok {
			entry.DisplayName = metadata.DisplayName
			if metadata.Enable != nil {
				entry.Enable = *metadata.Enable
			}
			if metadata.Free != nil {
				entry.Free = *metadata.Free
			}
			if metadata.Default != nil {
				entry.Default = *metadata.Default
			}
		} else if builtin, ok := qoderBuiltinModelByID(id); ok {
			entry.DisplayName = builtin.DisplayName
			entry.Free = builtin.Free
			entry.Default = builtin.Default
		}
		entries = append(entries, entry)
	}
	return entries
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

// qoderAccountToken 读取账号保存的 Qoder 用户令牌。API Key 账号把用户令牌存在
// api_key；设备流 OAuth 账号把设备令牌存在 access_token，均兼容历史字段。
func qoderAccountToken(account *Account) string {
	if account == nil {
		return ""
	}
	if token := strings.TrimSpace(account.GetCredential("access_token")); token != "" {
		return token
	}
	return strings.TrimSpace(account.GetCredential("api_key"))
}

// qoderAccountRefreshToken 读取设备流 OAuth 账号保存的 refresh_token。
func qoderAccountRefreshToken(account *Account) string {
	if account == nil {
		return ""
	}
	return strings.TrimSpace(account.GetCredential("refresh_token"))
}

// validateQoderCredentials 校验 Qoder 账号：只支持 apikey，令牌直连官方
// Model Server，协议固定 chat_completions。
func validateQoderCredentials(platform, accountType string, credentials map[string]any) error {
	if platform != PlatformQoder {
		return nil
	}
	// apikey：手动粘贴 Qoder 访问令牌；oauth：设备流授权得到的设备令牌 + refresh_token。
	switch accountType {
	case AccountTypeAPIKey:
		token, _ := credentials["api_key"].(string)
		if strings.TrimSpace(token) == "" {
			token, _ = credentials["access_token"].(string)
		}
		if strings.TrimSpace(token) == "" {
			return infraerrors.BadRequest("INVALID_QODER_CREDENTIALS", "qoder requires a Qoder access token")
		}
	case AccountTypeOAuth:
		token, _ := credentials["access_token"].(string)
		if strings.TrimSpace(token) == "" {
			token, _ = credentials["api_key"].(string)
		}
		if strings.TrimSpace(token) == "" {
			return infraerrors.BadRequest("INVALID_QODER_CREDENTIALS", "qoder oauth requires a device access token")
		}
	default:
		return infraerrors.BadRequest("INVALID_QODER_CREDENTIALS", "qoder requires an API key or OAuth account holding a Qoder access token")
	}
	protocol, _ := credentials["api_protocol"].(string)
	if protocol != "" && protocol != APIProtocolChatCompletions {
		return infraerrors.BadRequest("INVALID_QODER_CREDENTIALS", "qoder only supports the chat_completions upstream protocol")
	}
	return nil
}

// qoderQuotaDiagnosis 在上游 402 时补充套餐与额度事实，避免只看到
// “quota exceeded” 而误判为令牌失效。读取失败时静默返回空串，不影响原始错误。
func qoderQuotaDiagnosis(ctx context.Context, accessToken string) string {
	client := &qoder.Client{HTTPClient: &http.Client{Timeout: 15 * time.Second}, UserAgent: "qoder-cli"}
	status, err := client.FetchAccountStatus(ctx, accessToken)
	if err != nil || status == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if status.Plan != "" {
		parts = append(parts, "plan="+status.Plan)
	}
	if status.UserTag != "" {
		parts = append(parts, "tag="+status.UserTag)
	}
	if status.IsQuotaExceeded || status.Quota <= 0 {
		parts = append(parts, "quota exhausted")
	} else {
		parts = append(parts, fmt.Sprintf("quota=%d", status.Quota))
	}
	return " (Qoder account: " + strings.Join(parts, ", ") + ")"
}
// QoderCatalogSource 记录一次目录同步的来源，便于前端区分「真实套餐目录」与「内置回退」。
const (
	QoderCatalogSourceSigned  = "signed_catalog"
	QoderCatalogSourceBuiltin = "builtin"
)

// QoderCatalogFetcher 负责用官方签名请求拉取 Qoder 模型目录。
//
// 为什么需要它：Qoder 的 /api/v2/model/list 只接受官方客户端签名
// （Authorization: Bearer COSY.* + Cosy-* 头，路径带 /algo 前缀），
// 直接 Bearer 访问固定 404。签名逻辑是 Rust 编译的 WASM，Go 无法原生复用，
// 因此由可配置的 Node 侧车执行。侧车未配置或执行失败时，调用方回退内置目录。
type QoderCatalogFetcher interface {
	// FetchCatalog 返回原始目录 JSON（按 scene 分组的对象）。
	FetchCatalog(ctx context.Context, accessToken, uid string) ([]byte, error)
}

var qoderCatalogFetcher QoderCatalogFetcher

// SetQoderCatalogFetcher 注入签名目录侧车（由 wire 或测试调用）。
func SetQoderCatalogFetcher(fetcher QoderCatalogFetcher) { qoderCatalogFetcher = fetcher }

// CurrentQoderCatalogFetcher 返回当前侧车，未配置时为 nil。
func CurrentQoderCatalogFetcher() QoderCatalogFetcher { return qoderCatalogFetcher }
// parseQoderCatalog 解析签名目录：按 scene 分组，只保留字段齐全的条目。
//
// 官方响应是明文 JSON（不是加密的），字段名用 snake_case；这里对同一字段的
// camelCase 变体做兼容，避免上游改键后整份目录解析失败。
func parseQoderCatalog(body []byte) (*QoderCatalog, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse qoder catalog: %w", err)
	}
	catalog := &QoderCatalog{Source: QoderCatalogSourceSigned, Scenes: make(map[string][]QoderCatalogEntry)}
	for scene, payload := range raw {
		var items []map[string]any
		if err := json.Unmarshal(payload, &items); err != nil {
			continue
		}
		entries := make([]QoderCatalogEntry, 0, len(items))
		for _, item := range items {
			key := strings.TrimSpace(qoderStringField(item, "key", "model_key", "modelId", "id"))
			if key == "" {
				continue
			}
			entry := QoderCatalogEntry{
				Key:             key,
				DisplayName:     qoderStringField(item, "display_name", "displayName", "name"),
				Enable:          qoderBoolField(item, "enable", "enabled"),
				Free:            qoderBoolField(item, "is_free", "isFree"),
				Default:         qoderBoolField(item, "is_default", "isDefault"),
				Reasoning:       qoderBoolField(item, "is_reasoning", "isReasoning"),
				Vision:          qoderBoolField(item, "is_vl", "isVl"),
				ContextWindow:   qoderIntField(item, "max_input_tokens", "maxInputTokens"),
				MaxOutputTokens: qoderIntField(item, "max_output_tokens", "maxOutputTokens"),
				PriceFactor:     qoderFloatField(item, "price_factor", "priceFactor"),
			}
			if entry.DisplayName == "" {
				entry.DisplayName = key
			}
			entry.ContextWindows = qoderContextWindows(item)
			entry.Efforts, entry.DefaultEffort = qoderThinkingEfforts(item)
			entries = append(entries, entry)
		}
		if len(entries) > 0 {
			catalog.Scenes[scene] = entries
		}
	}
	if len(catalog.Scenes) == 0 {
		return nil, newUpstreamModelSyncUpstreamError("Qoder catalog response contained no scenes", nil)
	}
	return catalog, nil
}
func qoderStringField(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := item[key].(string); ok {
			return value
		}
	}
	return ""
}

func qoderBoolField(item map[string]any, keys ...string) bool {
	for _, key := range keys {
		switch value := item[key].(type) {
		case bool:
			return value
		case float64:
			return value != 0
		case string:
			return value == "1" || strings.EqualFold(value, "true")
		}
	}
	return false
}

func qoderIntField(item map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := item[key].(float64); ok {
			return int64(value)
		}
	}
	return 0
}

func qoderFloatField(item map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := item[key].(float64); ok {
			return value
		}
	}
	return 0
}
// qoderContextWindows 读取 context_config 里的可选上下文档位（去重升序）。
func qoderContextWindows(item map[string]any) []int64 {
	config, ok := item["context_config"].(map[string]any)
	if !ok {
		return nil
	}
	windows := make([]int64, 0, len(config))
	seen := make(map[int64]bool, len(config))
	for _, raw := range config {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		tokens, ok := entry["token_count"].(float64)
		if !ok || tokens <= 0 {
			continue
		}
		value := int64(tokens)
		if seen[value] {
			continue
		}
		seen[value] = true
		windows = append(windows, value)
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i] < windows[j] })
	return windows
}

// qoderThinkingEfforts 读取 thinking_config.enabled.efforts 的推理档位。
func qoderThinkingEfforts(item map[string]any) ([]string, string) {
	config, ok := item["thinking_config"].(map[string]any)
	if !ok {
		return nil, ""
	}
	enabled, ok := config["enabled"].(map[string]any)
	if !ok {
		return nil, ""
	}
	efforts, ok := enabled["efforts"].(map[string]any)
	if !ok {
		return nil, ""
	}
	levels := make([]string, 0, len(efforts))
	defaultLevel := ""
	for level, raw := range efforts {
		level = strings.TrimSpace(level)
		if level == "" {
			continue
		}
		levels = append(levels, level)
		if entry, ok := raw.(map[string]any); ok && qoderBoolField(entry, "is_default", "isDefault") {
			defaultLevel = level
		}
	}
	sort.Strings(levels)
	return levels, defaultLevel
}
// qoderCatalogToModelMetadata 把目录条目转换为可持久化的能力元数据。
func qoderCatalogToModelMetadata(entry QoderCatalogEntry) UpstreamModelMetadata {
	metadata := UpstreamModelMetadata{
		ID:               entry.Key,
		DisplayName:      entry.DisplayName,
		ContextWindow:    entry.ContextWindow,
		MaxContextWindow: entry.ContextWindow,
		MaxOutputTokens:  entry.MaxOutputTokens,
		Enable:           qoderBoolPtr(entry.Enable),
		Free:             qoderBoolPtr(entry.Free),
		Default:          qoderBoolPtr(entry.Default),
	}
	for _, window := range entry.ContextWindows {
		if window > metadata.MaxContextWindow {
			metadata.MaxContextWindow = window
		}
	}
	if entry.Reasoning {
		metadata.Reasoning = qoderBoolPtr(true)
	}
	if entry.Vision {
		metadata.InputModalities = []string{"text", "image"}
	} else {
		metadata.InputModalities = []string{"text"}
	}
	if len(entry.Efforts) > 0 {
		metadata.SupportedReasoningLevels = append([]string(nil), entry.Efforts...)
		metadata.DefaultReasoningLevel = entry.DefaultEffort
	}
	if entry.PriceFactor != 0 {
		price := entry.PriceFactor
		metadata.PriceFactor = &price
	}
	return metadata
}
// syncQoderSignedCatalog 用签名侧车拉取真实目录，并把结果写回账号快照。
//
// 侧车未配置时返回 (nil, nil)：调用方回退内置目录，且不写快照。
// 侧车失败时返回错误：同样回退，但把失败原因记进日志，不污染账号状态。
func (s *AccountTestService) syncQoderSignedCatalog(ctx context.Context, account *Account) (*UpstreamModelCatalog, error) {
	fetcher := CurrentQoderCatalogFetcher()
	if fetcher == nil {
		return nil, nil
	}
	token := qoderAccountToken(account)
	if strings.TrimSpace(token) == "" {
		return nil, nil
	}
	uid, err := s.resolveQoderCatalogUID(ctx, account)
	if err != nil {
		return nil, err
	}
	raw, err := fetcher.FetchCatalog(ctx, token, uid)
	if err != nil {
		return nil, err
	}
	catalog, err := parseQoderCatalog(raw)
	if err != nil {
		return nil, err
	}
	entries := catalog.ChatModels()
	if len(entries) == 0 {
		return nil, newUpstreamModelSyncUpstreamError("Qoder signed catalog has no chat scene", nil)
	}

	modelIDs := make([]string, 0, len(entries))
	metadata := make(map[string]UpstreamModelMetadata, len(entries))
	for _, entry := range entries {
		modelIDs = append(modelIDs, entry.Key)
		metadata[entry.Key] = qoderCatalogToModelMetadata(entry)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if account != nil && account.ID > 0 && s.accountRepo != nil {
		snapshot := UpstreamSupportedModelsSnapshot{Source: QoderCatalogSourceSigned, SyncedAt: now, Models: modelIDs}
		metaSnapshot := UpstreamModelMetadataSnapshot{Source: QoderCatalogSourceSigned, SyncedAt: now, Models: metadata}
		updates := map[string]any{
			UpstreamSupportedModelsExtraKey: snapshot,
			UpstreamModelMetadataExtraKey:   metaSnapshot,
		}
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, updates); err != nil {
			return nil, newUpstreamModelSyncInternalError("Failed to save Qoder model catalog", err)
		}
		account.SetUpstreamSupportedModelsSnapshot(snapshot)
		account.SetUpstreamModelMetadataSnapshot(metaSnapshot)
	}

	result := &UpstreamModelCatalog{Models: modelIDs, Metadata: metadata}
	enabled := 0
	for _, entry := range entries {
		if entry.Enable {
			enabled++
		}
	}
	result.Warnings = append(result.Warnings, UpstreamModelSyncWarning{
		Code: QoderCatalogSyncedWarningCode,
		Message: fmt.Sprintf("Synced Qoder signed catalog: %d chat models, %d enabled for this account's plan.",
			len(entries), enabled),
	})
	if enabled == 0 {
		result.Warnings = append(result.Warnings, UpstreamModelSyncWarning{
			Code: "qoder_catalog_no_enabled_models",
			Message: "No Qoder chat model is enabled for this account's plan right now; " +
				"upgrade the Qoder plan or check quota/entitlement before scheduling this account.",
		})
	}
	return result, nil
}

// QoderUserInfoURL 是 Qoder 账号信息接口，uid 缺失时用来补齐签名上下文。
var QoderUserInfoURL = "https://openapi.qoder.sh/api/v1/userinfo"

// resolveQoderCatalogUID 返回签名上下文需要的 uid，缺失时向 Qoder userinfo 补齐。
//
// 设备流授权只写 access/refresh token，历史账号没有 uid；签名上下文缺 uid
// 会被上游判为 Signature invalid（403 code 101）。官方 /api/v1/userinfo 返回
// 的 id 就是 uid（账号 #855 实测 6406f467-...），取到后缓存在本次账号对象上。
func (s *AccountTestService) resolveQoderCatalogUID(ctx context.Context, account *Account) (string, error) {
	if uid := qoderAccountUID(account); uid != "" {
		return uid, nil
	}
	token := qoderAccountToken(account)
	if strings.TrimSpace(token) == "" {
		return "", nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, QoderUserInfoURL, nil)
	if err != nil {
		return "", newUpstreamModelSyncInternalError("Failed to build Qoder userinfo request", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := qoderCatalogHTTPClient.Do(request)
	if err != nil {
		return "", newUpstreamModelSyncUpstreamError("Qoder userinfo request failed: "+err.Error(), err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", newUpstreamModelSyncUpstreamError("Qoder userinfo read failed: "+err.Error(), err)
	}
	if response.StatusCode != http.StatusOK {
		message := strings.TrimSpace(string(body))
		if len(message) > 200 {
			message = message[:200]
		}
		return "", newUpstreamModelSyncUpstreamError(
			fmt.Sprintf("Qoder userinfo returned HTTP %d: %s", response.StatusCode, message), nil)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", newUpstreamModelSyncUpstreamError("Qoder userinfo returned invalid JSON", err)
	}
	uid := strings.TrimSpace(qoderStringField(payload, "id", "user_id", "uid"))
	if uid == "" {
		return "", newUpstreamModelSyncUpstreamError("Qoder userinfo did not return a user id", nil)
	}
	// 不持久化：AccountRepository 没有暴露 credentials 的原子更新接口，整体
	// Update 会覆盖并发的 token 刷新。只在本次内存对象上补上，供签名使用。
	if account != nil {
		if account.Credentials == nil {
			account.Credentials = map[string]any{}
		}
		account.Credentials["uid"] = uid
	}
	return uid, nil
}

// qoderCatalogHTTPClient 用于 userinfo 这类轻量请求；可由测试替换。
var qoderCatalogHTTPClient = &http.Client{Timeout: 20 * time.Second}

// qoderAccountUID 读取账号的 Qoder uid；签名上下文的 Cosy-User 依赖它。
func qoderAccountUID(account *Account) string {
	if account == nil {
		return ""
	}
	for _, key := range []string{"uid", "user_id", "qoder_uid"} {
		if value := strings.TrimSpace(account.GetCredential(key)); value != "" {
			return value
		}
	}
	return ""
}

// QoderCatalogSidecarCommand 是签名目录侧车的执行入口，测试可替换。
var QoderCatalogSidecarCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// QoderCatalogSidecarFetcher 通过 Node 侧车执行官方 WASM 签名，拉取真实目录。
//
// 侧车脚本见 tools/qoder-catalog/fetch.mjs：stdin 一行 JSON，stdout 一行 JSON。
type QoderCatalogSidecarFetcher struct {
	// ScriptPath 是 fetch.mjs 的绝对路径。
	ScriptPath string
	// LibPath 是已切分好的 wasm-lib.mjs（导出 __initWasm / QoderContext）。
	LibPath string
	// NodeBin 默认 node。
	NodeBin string
	// Args 追加在脚本路径之后（仅 stdio 模式使用）。
	Args []string
	// QueueDir 非空时走文件队列：主进程写 <QueueDir>/req.json，侧车
	// serve.mjs 写 <QueueDir>/resp.json。生产走这条路，因为 docker exec 的
	// stdout 在 64KB 处被截断，约 77KB 的目录响应会被切坏。
	QueueDir string
	// Timeout 覆盖侧车超时，默认 30s。
	Timeout time.Duration
}

// qoderCatalogResponse 是侧车响应文件的结构。
type qoderCatalogResponse struct {
	OK    bool   `json:"ok"`
	Body  string `json:"body"`
	Error string `json:"error"`
}

func (f *QoderCatalogSidecarFetcher) FetchCatalog(ctx context.Context, accessToken, uid string) ([]byte, error) {
	if f == nil || strings.TrimSpace(f.ScriptPath) == "" {
		return nil, newUpstreamModelSyncUpstreamError("Qoder signed catalog sidecar is not configured", nil)
	}
	nodeBin := f.NodeBin
	if strings.TrimSpace(nodeBin) == "" {
		nodeBin = "node"
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(map[string]string{"access_token": accessToken, "uid": uid, "scene": QoderCatalogScene})
	if err != nil {
		return nil, err
	}
	if dir := strings.TrimSpace(f.QueueDir); dir != "" {
		// 队列模式由侧车进程读取 QODER_WASM_LIB；主进程只投递请求。
		return f.fetchViaQueue(runCtx, dir, payload)
	}
	cmd := QoderCatalogSidecarCommand(runCtx, nodeBin, append([]string{f.ScriptPath}, f.Args...)...)
	cmd.Stdin = bytes.NewReader(append(payload, '\n'))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if strings.TrimSpace(f.LibPath) != "" {
		cmd.Env = append(os.Environ(), "QODER_WASM_LIB="+f.LibPath)
	}
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if len(message) > 500 {
			message = message[:500]
		}
		return nil, newUpstreamModelSyncUpstreamError(
			fmt.Sprintf("Qoder catalog sidecar failed: %v %s", err, message), err)
	}
	var result struct {
		OK    bool   `json:"ok"`
		Body  string `json:"body"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		return nil, newUpstreamModelSyncUpstreamError("Qoder catalog sidecar returned invalid JSON", err)
	}
	if !result.OK {
		return nil, newUpstreamModelSyncUpstreamError("Qoder catalog sidecar error: "+result.Error, nil)
	}
	if strings.TrimSpace(result.Body) == "" {
		return nil, newUpstreamModelSyncUpstreamError("Qoder catalog sidecar returned empty body", nil)
	}
	return []byte(result.Body), nil
}

// fetchViaQueue 通过共享目录与侧车交换请求/响应文件。
//
// 协议见 tools/qoder-catalog/serve.mjs：请求先写临时文件再 rename 成
// req.json（原子可见）；响应以 mtime 晚于本次请求写入时刻为准，避免读到
// 上一次请求留下的旧结果。
func (f *QoderCatalogSidecarFetcher) fetchViaQueue(ctx context.Context, dir string, payload []byte) ([]byte, error) {
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return nil, newUpstreamModelSyncUpstreamError("Qoder catalog queue dir unavailable: "+err.Error(), err)
	}
	// 容器内目录由卷挂载，权限可能来自镜像（0755）；尽力放开，失败不影响主流程，
	// 侧车自身也会 chmod。
	_ = os.Chmod(dir, 0o777)

	reqFile := filepath.Join(dir, "req.json")
	respFile := filepath.Join(dir, "resp.json")

	reqTmp := filepath.Join(dir, fmt.Sprintf("req-%d-%s.tmp", time.Now().UnixNano(), uuid.NewString()))
	if err := os.WriteFile(reqTmp, payload, 0o666); err != nil {
		return nil, newUpstreamModelSyncUpstreamError("Qoder catalog queue write failed: "+err.Error(), err)
	}
	// 写请求前先清掉旧响应，后续只接受 mtime 不早于本次写入的响应。
	_ = os.Remove(respFile)
	started := time.Now()
	if err := os.Rename(reqTmp, reqFile); err != nil {
		_ = os.Remove(reqTmp)
		return nil, newUpstreamModelSyncUpstreamError("Qoder catalog queue dispatch failed: "+err.Error(), err)
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if raw, ok := readQoderCatalogQueueResponse(respFile, started); ok {
			_ = os.Remove(respFile)
			return decodeQoderCatalogResponse(raw)
		}
		select {
		case <-ctx.Done():
			return nil, newUpstreamModelSyncUpstreamError("Qoder catalog sidecar timed out", ctx.Err())
		case <-ticker.C:
		}
	}
}

// readQoderCatalogQueueResponse 读取满足条件（mtime 不早于 started）的响应。
func readQoderCatalogQueueResponse(respFile string, started time.Time) ([]byte, bool) {
	info, err := os.Stat(respFile)
	if err != nil || info.ModTime().Before(started.Add(-time.Second)) {
		return nil, false
	}
	raw, err := os.ReadFile(respFile)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	return raw, true
}

// decodeQoderCatalogResponse 解析并校验侧车响应。
func decodeQoderCatalogResponse(raw []byte) ([]byte, error) {
	var result qoderCatalogResponse
	if err := json.Unmarshal(bytes.TrimSpace(raw), &result); err != nil {
		return nil, newUpstreamModelSyncUpstreamError("Qoder catalog sidecar returned invalid JSON", err)
	}
	if !result.OK {
		return nil, newUpstreamModelSyncUpstreamError("Qoder catalog sidecar error: "+result.Error, nil)
	}
	if strings.TrimSpace(result.Body) == "" {
		return nil, newUpstreamModelSyncUpstreamError("Qoder catalog sidecar returned empty body", nil)
	}
	return []byte(result.Body), nil
}
