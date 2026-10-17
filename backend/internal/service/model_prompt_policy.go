package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// SettingKeyModelSystemPrompts 是按模型追加 system 提示词的运行时配置。
const SettingKeyModelSystemPrompts = "model_system_prompts"

// ModelSystemPromptEntry 把一段提示词绑定到一个精确的模型 ID。
// 匹配发生在别名归一之后，所以这里写 canonical ID 即可覆盖所有别名写法。
type ModelSystemPromptEntry struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// ModelSystemPromptPolicy 是模型 → system 提示词的映射集合。
type ModelSystemPromptPolicy struct {
	Entries []ModelSystemPromptEntry `json:"entries"`
}

// Validate 只约束结构安全：非空、唯一、长度上限；提示词内容不做语义校验。
func (p *ModelSystemPromptPolicy) Validate() error {
	if p == nil || len(p.Entries) > 512 {
		return fmt.Errorf("at most 512 model system prompts are allowed")
	}
	seen := make(map[string]bool, len(p.Entries))
	for _, entry := range p.Entries {
		model := entry.Model
		if model == "" || model != strings.TrimSpace(model) || len(model) > 200 || strings.ContainsAny(model, " \t\n\r*") || seen[model] {
			return fmt.Errorf("model IDs must be nonempty, exact and unique: %q", entry.Model)
		}
		if strings.TrimSpace(entry.Prompt) == "" || len(entry.Prompt) > 20000 {
			return fmt.Errorf("prompt for model %q must be nonempty and at most 20000 characters", model)
		}
		seen[model] = true
	}
	return nil
}

// PromptFor 返回模型对应的提示词；未配置时返回空串。
func (p *ModelSystemPromptPolicy) PromptFor(model string) string {
	if p == nil {
		return ""
	}
	for _, entry := range p.Entries {
		if entry.Model == model {
			return entry.Prompt
		}
	}
	return ""
}

// cloneModelSystemPromptPolicy 深拷贝提示词策略，避免进程内降级快照与调用方共享底层切片。
func cloneModelSystemPromptPolicy(policy *ModelSystemPromptPolicy) *ModelSystemPromptPolicy {
	if policy == nil {
		return nil
	}
	clone := &ModelSystemPromptPolicy{Entries: slices.Clone(policy.Entries)}
	return clone
}

// GetModelSystemPromptPolicy 读取按模型注入的 system 提示词策略。
// 与模型别名在同一次入口绑定，读库失败时沿用最近一次成功解析的快照，避免同一入口
// 因 DB 瞬时故障产生与别名一致的 503；解码/校验失败与冷启动仍显式报错。
func (s *SettingService) GetModelSystemPromptPolicy(ctx context.Context) (*ModelSystemPromptPolicy, error) {
	if policy := ModelSystemPromptsFromContext(ctx); policy != nil {
		return policy, nil
	}
	if s == nil || s.settingRepo == nil {
		return &ModelSystemPromptPolicy{}, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyModelSystemPrompts)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		empty := &ModelSystemPromptPolicy{}
		s.storeModelSystemPromptPolicy(empty)
		return empty, nil
	}
	if err != nil {
		if cached := s.cachedModelSystemPromptPolicy(); cached != nil {
			s.logModelPolicyFallback(SettingKeyModelSystemPrompts, err)
			return cached, nil
		}
		return nil, err
	}
	var policy ModelSystemPromptPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return nil, fmt.Errorf("decode model system prompts: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	s.storeModelSystemPromptPolicy(&policy)
	return &policy, nil
}

func (s *SettingService) storeModelSystemPromptPolicy(policy *ModelSystemPromptPolicy) {
	s.modelSystemPromptPolicyCache.Store(cloneModelSystemPromptPolicy(policy))
}

func (s *SettingService) cachedModelSystemPromptPolicy() *ModelSystemPromptPolicy {
	cached, _ := s.modelSystemPromptPolicyCache.Load().(*ModelSystemPromptPolicy)
	return cloneModelSystemPromptPolicy(cached)
}

func (s *SettingService) SetModelSystemPromptPolicy(ctx context.Context, policy *ModelSystemPromptPolicy) error {
	if policy == nil {
		policy = &ModelSystemPromptPolicy{}
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyModelSystemPrompts, string(data))
}

type modelSystemPromptContextKey struct{}

// 每个请求固定同一份设置；SQL/API 修改在后续请求生效。
func WithModelSystemPrompts(ctx context.Context, policy *ModelSystemPromptPolicy) context.Context {
	return context.WithValue(ctx, modelSystemPromptContextKey{}, policy)
}

func ModelSystemPromptsFromContext(ctx context.Context) *ModelSystemPromptPolicy {
	policy, _ := ctx.Value(modelSystemPromptContextKey{}).(*ModelSystemPromptPolicy)
	return policy
}
