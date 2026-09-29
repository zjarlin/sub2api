package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const SettingKeyAutoModelPolicy = "auto_model_policy"

type AutoModelPolicy struct {
	Blacklist []string `json:"blacklist"`
}

func DefaultAutoModelPolicy() *AutoModelPolicy {
	return &AutoModelPolicy{Blacklist: []string{"doubao*"}}
}

func (p *AutoModelPolicy) Validate() error {
	if p == nil || p.Blacklist == nil || len(p.Blacklist) > 128 {
		return fmt.Errorf("blacklist must be an array with at most 128 patterns")
	}
	seen := make(map[string]bool)
	for _, pattern := range p.Blacklist {
		normalized := strings.ToLower(pattern)
		if pattern == "" || len(pattern) > 200 || strings.ContainsAny(pattern, " \t\r\n?[]\\") ||
			strings.Contains(strings.TrimSuffix(pattern, "*"), "*") || seen[normalized] {
			return fmt.Errorf("blacklist patterns must be unique model IDs with an optional trailing *: %q", pattern)
		}
		seen[normalized] = true
	}
	return nil
}

// 不带供应商前缀的规则也匹配最后一个斜杠后的模型名，覆盖供应商命名空间。
func (p *AutoModelPolicy) excludes(model string) bool {
	model = strings.ToLower(model)
	for _, pattern := range p.Blacklist {
		pattern = strings.ToLower(pattern)
		if matchWildcard(pattern, model) {
			return true
		}
		if !strings.Contains(pattern, "/") && matchWildcard(pattern, model[strings.LastIndex(model, "/")+1:]) {
			return true
		}
	}
	return false
}

func (s *SettingService) GetAutoModelPolicy(ctx context.Context) (*AutoModelPolicy, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultAutoModelPolicy(), nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAutoModelPolicy)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		return DefaultAutoModelPolicy(), nil
	}
	if err != nil {
		return nil, err
	}
	var policy AutoModelPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return nil, fmt.Errorf("decode auto model policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &policy, nil
}

func (s *SettingService) SetAutoModelPolicy(ctx context.Context, policy *AutoModelPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyAutoModelPolicy, string(data))
}
