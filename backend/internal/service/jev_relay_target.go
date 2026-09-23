package service

import (
	"context"
	"net/http"
	"strings"
)

// TypeSafeRelayTarget 返回 TypeSafe 中继所需的上游地址、可用 API Key 与 HTTP 客户端。
//
// 该文件是 jev_api 与内容审计服务之间的最小适配层：复用 TypeSafe 引擎档案的
// base_url、Key 池与代理配置，不修改 content_moderation 的既有行为。
// 密钥仅交给中继函数用于设置 Authorization，不写日志、不回传浏览器。
func (s *ContentModerationService) TypeSafeRelayTarget(ctx context.Context) (string, string, *http.Client, error) {
	if s == nil || s.settingRepo == nil {
		return "", "", nil, nil
	}
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return "", "", nil, err
	}
	effective := cfg.effectiveEngine(ContentModerationEngineTypeSafe)
	key, _ := s.nextUsableAPIKey(effective)
	client, err := s.moderationHTTPClient(ctx, effective)
	if err != nil {
		return "", "", nil, err
	}
	return strings.TrimSpace(effective.BaseURL), key, client, nil
}
