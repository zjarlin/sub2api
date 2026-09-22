package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const SettingKeyVisionFallbackPolicy = "vision_fallback_policy"

// Models 是助手的尝试顺序，与主模型的能力档位相互独立。
type VisionFallbackPolicy struct {
	Enabled                 bool     `json:"enabled"`
	Models                  []string `json:"models"`
	AllowUnlistedModels     bool     `json:"allow_unlisted_models"`
	CandidateTimeoutSeconds int      `json:"candidate_timeout_seconds"`
	TimeoutSeconds          int      `json:"timeout_seconds"`
}

func DefaultVisionFallbackPolicy(cfg *config.Config) *VisionFallbackPolicy {
	p := &VisionFallbackPolicy{
		Enabled: visionFallbackEnabled(cfg), Models: []string{}, AllowUnlistedModels: true,
		CandidateTimeoutSeconds: int(visionHelperTimeout / time.Second), TimeoutSeconds: int(visionFallbackTimeout / time.Second),
	}
	if cfg != nil {
		legacy := cfg.Gateway.VisionFallback
		if model := strings.TrimSpace(legacy.Model); model != "" {
			p.Models = []string{model}
		}
		if legacy.CandidateTimeoutSeconds > 0 {
			p.CandidateTimeoutSeconds = legacy.CandidateTimeoutSeconds
		}
		if legacy.TimeoutSeconds > 0 {
			p.TimeoutSeconds = legacy.TimeoutSeconds
		}
	}
	return p
}

func (p *VisionFallbackPolicy) Validate() error {
	if p == nil || len(p.Models) > 64 {
		return fmt.Errorf("vision fallback supports at most 64 ordered models")
	}
	if p.Enabled && !p.AllowUnlistedModels && len(p.Models) == 0 {
		return fmt.Errorf("configure at least one vision model or allow unlisted models")
	}
	seen := make(map[string]bool)
	for _, model := range p.Models {
		if model == "" || len(model) > 200 || strings.ContainsAny(model, " \t\n\r*") || seen[model] {
			return fmt.Errorf("vision model IDs must be unique, nonempty and exact: %q", model)
		}
		seen[model] = true
	}
	// 仅限制 duration 可表示的范围，不替用户决定模型顺序或超时大小。
	const maxSeconds = int64((1<<63 - 1) / int64(time.Second))
	for _, seconds := range []int{p.CandidateTimeoutSeconds, p.TimeoutSeconds} {
		if seconds <= 0 || int64(seconds) > maxSeconds {
			return fmt.Errorf("vision timeouts must be positive seconds within %d", maxSeconds)
		}
	}
	return nil
}

func (s *SettingService) GetVisionFallbackPolicy(ctx context.Context) (*VisionFallbackPolicy, error) {
	var cfg *config.Config
	if s != nil {
		cfg = s.cfg
	}
	return loadVisionFallbackPolicy(ctx, s, cfg)
}

// 无持久化配置时兼容环境变量；已保存的策略直接生效，包括显式关闭。
func loadVisionFallbackPolicy(ctx context.Context, settings *SettingService, cfg *config.Config) (*VisionFallbackPolicy, error) {
	if settings == nil || settings.settingRepo == nil {
		return DefaultVisionFallbackPolicy(cfg), nil
	}
	raw, err := settings.settingRepo.GetValue(ctx, SettingKeyVisionFallbackPolicy)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		return DefaultVisionFallbackPolicy(cfg), nil
	}
	if err != nil {
		return nil, err
	}
	var policy VisionFallbackPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return nil, fmt.Errorf("decode vision fallback policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if policy.Models == nil {
		policy.Models = []string{}
	}
	return &policy, nil
}

func (s *SettingService) SetVisionFallbackPolicy(ctx context.Context, policy *VisionFallbackPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyVisionFallbackPolicy, string(data))
}
