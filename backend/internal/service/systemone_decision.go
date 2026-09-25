package service

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// System One 决策模型平台：Laya 与 JEV 共用同一份 wire protocol 与同一组问句语义，
// 差别只在上游归属（本地 edge-laya vs 远端 TypeSafe/CommandCode）。
//
// 两个平台都走内置适配器模式：账号地址与共享密钥由后端注入，协议固定 systemone。
// Laya 与 JEV 的默认模型名不同，但请求形状一致：
//
//	{"model": "<模型名>", "state": ..., "questions": {"<qid>": {"type", "instructions", "criteria"}}}
//
// 返回结构一致：answers[<qid>] 携带 probabilities / confidence，usage.output_tokens 恒为 0。

// 默认模型名。Laya 用公开名 "laya"（由适配器按语言自动选检查点）；
// JEV 沿用上游的 "typesafe/jev"。
const (
	DefaultLayaModel = "laya"
	DefaultJevModel  = "typesafe/jev"
)

func DefaultLayaModelIDs() []string { return []string{DefaultLayaModel, "laya-english", "laya-multilingual"} }

func DefaultJevModelIDs() []string { return []string{DefaultJevModel} }

func (a *Account) IsLaya() bool { return a != nil && a.Platform == PlatformLaya }

func (a *Account) IsJev() bool { return a != nil && a.Platform == PlatformJev }

// IsSystemOneDecisionPlatform 报告平台是否为 System One 决策模型。
func IsSystemOneDecisionPlatform(platform string) bool {
	return platform == PlatformLaya || platform == PlatformJev
}

// validateSystemOneDecisionCredentials 校验 System One 决策模型账号。
// 与 ZCode 等内置适配器一致：只支持 apikey，地址与共享密钥由后端注入，
// 协议固定 systemone（决策模型不生成文本，不存在 chat/responses 变体）。
func validateSystemOneDecisionCredentials(platform, accountType string, credentials map[string]any) error {
	if !IsSystemOneDecisionPlatform(platform) {
		return nil
	}
	label := "laya"
	if platform == PlatformJev {
		label = "jev"
	}
	if accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest(
			"INVALID_SYSTEMONE_CREDENTIALS",
			label+" requires an API key account connected to a built-in System One adapter",
		)
	}
	if !BuiltinAdapterEnabled() {
		return infraerrors.BadRequest(
			"INVALID_SYSTEMONE_CREDENTIALS",
			label+" requires the built-in System One adapter; enable it in the server configuration",
		)
	}
	key, _ := credentials["api_key"].(string)
	// 本地 Laya 默认仅接受内网请求，未配置 LAYA_API_KEY 时无需上游密钥。
	if platform == PlatformJev && strings.TrimSpace(key) == "" {
		return infraerrors.BadRequest(
			"INVALID_SYSTEMONE_CREDENTIALS",
			label+" requires the adapter API key",
		)
	}
	protocol, _ := credentials["api_protocol"].(string)
	if protocol != "" && protocol != APIProtocolSystemOne {
		return infraerrors.BadRequest(
			"INVALID_SYSTEMONE_CREDENTIALS",
			label+" only supports the systemone upstream protocol",
		)
	}
	return nil
}
