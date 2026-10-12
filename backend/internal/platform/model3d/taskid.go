package model3d

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// TaskHandle 是网关签发给客户端的任务句柄明文。
//
// 上游任务 ID 由服务商生成，直接暴露会让任意用户通过猜测/枚举访问他人任务；
// 因此网关把它包进带签名的句柄，并绑定创建者与创建时使用的 API Key，
// 查询、取消、下载产物都必须携带同一个 API Key 才能通过校验。
type TaskHandle struct {
	// Provider 是服务商 key。
	Provider string `json:"p"`
	// Mode 是输入形态；Meshy 不同形态的任务查询路径不同，必须随句柄传递。
	Mode Mode `json:"m"`
	// TaskID 是上游任务 ID。
	TaskID string `json:"t"`
	// UserID/KeyID 绑定创建者，防止跨用户越权访问。
	UserID int64 `json:"u"`
	KeyID  int64 `json:"k"`
	// AccountID 与 GroupID 固定实际账号和创建时分组；Model 固定用量归属。
	AccountID int64  `json:"a,omitempty"`
	GroupID   int64  `json:"g,omitempty"`
	Model     string `json:"model,omitempty"`
	// CredentialIdentity 绑定账号凭据身份，防止任务被送往替换后的其他上游账户。
	CredentialIdentity string `json:"ci,omitempty"`
	// IssuedAt/ExpiresAt 是句柄签发与过期时间（Unix 秒）。
	IssuedAt  int64 `json:"ia"`
	ExpiresAt int64 `json:"ea"`
}

// Expired 判断句柄是否已过期。
func (h TaskHandle) Expired(now time.Time) bool {
	return h.ExpiresAt > 0 && now.Unix() >= h.ExpiresAt
}

// HandleSecret 从服务端密钥派生出任务句柄签名密钥，避免直接复用 JWT 密钥原文。
func HandleSecret(serverSecret string) []byte {
	mac := hmac.New(sha256.New, []byte(serverSecret))
	mac.Write([]byte("sub2api/model3d/task-handle/v1"))
	return mac.Sum(nil)
}

// EncodeTaskHandle 生成 "<payload>.<signature>" 形式的不透明句柄（均为 base64url 无填充）。
func EncodeTaskHandle(secret []byte, handle TaskHandle) (string, error) {
	if len(secret) == 0 {
		return "", ErrTaskMismatch
	}
	payload, err := json.Marshal(handle)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + signHandle(secret, encoded), nil
}

// DecodeTaskHandle 校验签名并还原句柄；签名不匹配、格式非法或已过期时返回错误。
func DecodeTaskHandle(secret []byte, token string, now time.Time) (TaskHandle, error) {
	if len(secret) == 0 {
		return TaskHandle{}, ErrTaskMismatch
	}
	token = strings.TrimSpace(token)
	dot := strings.LastIndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return TaskHandle{}, ErrTaskMismatch
	}
	encoded, signature := token[:dot], token[dot+1:]
	expected := signHandle(secret, encoded)
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return TaskHandle{}, ErrTaskMismatch
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return TaskHandle{}, ErrTaskMismatch
	}
	var handle TaskHandle
	if err := json.Unmarshal(payload, &handle); err != nil {
		return TaskHandle{}, ErrTaskMismatch
	}
	if strings.TrimSpace(handle.Provider) == "" || strings.TrimSpace(handle.TaskID) == "" {
		return TaskHandle{}, ErrTaskMismatch
	}
	if handle.Expired(now) {
		return TaskHandle{}, ErrTaskExpired
	}
	return handle, nil
}

func signHandle(secret []byte, encodedPayload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
