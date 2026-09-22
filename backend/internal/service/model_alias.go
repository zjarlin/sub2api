package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const SettingKeyModelAliases = "model_aliases"

type ModelAliasGroup struct {
	Canonical string   `json:"canonical"`
	Aliases   []string `json:"aliases"`
}

type ModelAliasPolicy struct {
	Groups []ModelAliasGroup `json:"groups"`
}

// 别名必须唯一归属，禁止循环、链式引用和通配符；不推断模型版本等价关系。
func (p *ModelAliasPolicy) Validate() error {
	if p == nil || len(p.Groups) > 256 {
		return fmt.Errorf("at most 256 alias groups are allowed")
	}
	seen := map[string]bool{}
	for _, group := range p.Groups {
		for _, id := range append([]string{group.Canonical}, group.Aliases...) {
			if id == "" || len(id) > 200 || strings.ContainsAny(id, " \t\n\r*") || seen[id] {
				return fmt.Errorf("model IDs must be nonempty, exact and unique: %q", id)
			}
			seen[id] = true
		}
	}
	if len(seen) > 2048 {
		return fmt.Errorf("at most 2048 model IDs are allowed")
	}
	return nil
}

func (p *ModelAliasPolicy) Canonicalize(model string) string {
	if p == nil {
		return model
	}
	for _, group := range p.Groups {
		if model == group.Canonical {
			return group.Canonical
		}
		for _, alias := range group.Aliases {
			if model == alias {
				return group.Canonical
			}
		}
	}
	return model
}

func (s *SettingService) GetModelAliasPolicy(ctx context.Context) (*ModelAliasPolicy, error) {
	if policy := ModelAliasesFromContext(ctx); policy != nil {
		return policy, nil
	}
	if s == nil || s.settingRepo == nil {
		return &ModelAliasPolicy{}, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyModelAliases)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		return &ModelAliasPolicy{}, nil
	}
	if err != nil {
		return nil, err
	}
	var policy ModelAliasPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return nil, fmt.Errorf("decode model aliases: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &policy, nil
}

func (s *SettingService) SetModelAliasPolicy(ctx context.Context, policy *ModelAliasPolicy) error {
	if policy == nil {
		policy = &ModelAliasPolicy{}
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyModelAliases, string(data))
}

type modelAliasContextKey struct{}

// 每个请求固定同一份设置；SQL/API 修改在后续请求生效，不污染共享账号快照。
func WithModelAliases(ctx context.Context, policy *ModelAliasPolicy) context.Context {
	return context.WithValue(ctx, modelAliasContextKey{}, policy)
}

func ModelAliasesFromContext(ctx context.Context) *ModelAliasPolicy {
	policy, _ := ctx.Value(modelAliasContextKey{}).(*ModelAliasPolicy)
	return policy
}

func (p *ModelAliasPolicy) IDs(model string) []string {
	if p != nil {
		for _, group := range p.Groups {
			if model == group.Canonical || slices.Contains(group.Aliases, model) {
				return append([]string{group.Canonical}, group.Aliases...)
			}
		}
	}
	return []string{model}
}

func (p *ModelAliasPolicy) CanonicalIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = p.Canonicalize(id)
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

// 只使用该账号的显式映射或目录证据，规范名优先，随后按别名配置顺序选择。
// cn:/global: 原样保留在最终目标，不向账号凭据或数据库复制映射。
func accountWithModelAliases(ctx context.Context, account *Account) *Account {
	policy := ModelAliasesFromContext(ctx)
	if account == nil || policy == nil || len(policy.Groups) == 0 {
		return account
	}
	clone := *account
	clone.globalModelMapping = nil
	native := clone.GetModelMapping()
	overlay := make(map[string]string)
	for _, group := range policy.Groups {
		for _, id := range policy.IDs(group.Canonical) {
			target, explicit := native[id]
			if clone.IsOpenAIPassthroughEnabled() {
				target = id
			}
			if !explicit {
				// 目录证据必须按实际 ID 精确匹配，避免把 TRAE 的大小写目标改成规范名。
				snapshot := clone.GetUpstreamSupportedModelsSnapshot()
				if snapshot == nil || !slices.Contains(snapshot.Models, id) {
					continue
				}
				target = id
			}
			if target == "" || !clone.IsModelSupported(id) {
				continue
			}
			// 不递归映射目标，已有账号映射的优先级高于全局别名。
			overlay[group.Canonical] = target
			break
		}
	}
	clone.globalModelMapping = overlay
	return &clone
}

func accountsWithModelAliases(ctx context.Context, accounts []Account) []Account {
	if ModelAliasesFromContext(ctx) == nil {
		return accounts
	}
	out := make([]Account, len(accounts))
	for i := range accounts {
		out[i] = *accountWithModelAliases(ctx, &accounts[i])
	}
	return out
}

// 已保存的旧档位也归一化去重，首次出现的档位与顺序优先。
func (p *ModelAliasPolicy) NormalizeFallback(policy *ModelFallbackPolicy) *ModelFallbackPolicy {
	out := &ModelFallbackPolicy{Enabled: policy.Enabled, Tiers: []ModelCapabilityTier{}}
	seen := map[string]bool{}
	for _, tier := range policy.Tiers {
		next := ModelCapabilityTier{Name: tier.Name}
		for _, model := range tier.Models {
			canonical := p.Canonicalize(model)
			if seen[canonical] {
				continue
			}
			seen[canonical] = true
			next.Models = append(next.Models, canonical)
		}
		if len(next.Models) > 0 {
			out.Tiers = append(out.Tiers, next)
		}
	}
	return out
}
