package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

const SettingKeyModelAliases = "model_aliases"

type ModelAliasGroup struct {
	Canonical string            `json:"canonical"`
	Aliases   []string          `json:"aliases"`
	Parent    string            `json:"parent,omitempty"`
	Routes    []ModelAliasRoute `json:"routes,omitempty"`
}

type ModelAliasPolicy struct {
	Groups []ModelAliasGroup `json:"groups"`
}

// 别名唯一归属；父子关系与渠道约束由树校验负责，禁止循环和通配符。
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
	return p.validateTree()
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

// cloneModelAliasPolicy 深拷贝别名策略，避免进程内降级快照与调用方共享底层切片。
func cloneModelAliasPolicy(policy *ModelAliasPolicy) *ModelAliasPolicy {
	if policy == nil {
		return nil
	}
	clone := &ModelAliasPolicy{Groups: make([]ModelAliasGroup, len(policy.Groups))}
	for i, group := range policy.Groups {
		clone.Groups[i] = group
		clone.Groups[i].Aliases = slices.Clone(group.Aliases)
		clone.Groups[i].Routes = slices.Clone(group.Routes)
		for j := range group.Routes {
			clone.Groups[i].Routes[j].BaseURLs = slices.Clone(group.Routes[j].BaseURLs)
		}
	}
	return clone
}

// GetModelAliasPolicy 读取全局模型别名策略。
// 网关文本入口每个请求都会读取该配置，读库失败时沿用最近一次成功解析的快照，
// 避免一次瞬时 DB 故障被放大成整个网关的 503。解码/校验失败与冷启动（从未成功
// 读取过）仍显式报错，不掩盖真正的配置缺陷。
func (s *SettingService) GetModelAliasPolicy(ctx context.Context) (*ModelAliasPolicy, error) {
	if policy := ModelAliasesFromContext(ctx); policy != nil {
		return policy, nil
	}
	if s == nil || s.settingRepo == nil {
		return &ModelAliasPolicy{}, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyModelAliases)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		empty := &ModelAliasPolicy{}
		s.storeModelAliasPolicy(empty)
		return empty, nil
	}
	if err != nil {
		if cached := s.cachedModelAliasPolicy(); cached != nil {
			s.logModelPolicyFallback(SettingKeyModelAliases, err)
			return cached, nil
		}
		return nil, err
	}
	var policy ModelAliasPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return nil, fmt.Errorf("decode model aliases: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	s.storeModelAliasPolicy(&policy)
	return &policy, nil
}

func (s *SettingService) storeModelAliasPolicy(policy *ModelAliasPolicy) {
	s.modelAliasPolicyCache.Store(cloneModelAliasPolicy(policy))
}

func (s *SettingService) cachedModelAliasPolicy() *ModelAliasPolicy {
	cached, _ := s.modelAliasPolicyCache.Load().(*ModelAliasPolicy)
	return cloneModelAliasPolicy(cached)
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
	ctx = context.WithValue(ctx, modelAliasContextKey{}, policy)
	if _, ok := ctx.Value(modelAliasAttemptsKey{}).(*sync.Map); !ok {
		ctx = context.WithValue(ctx, modelAliasAttemptsKey{}, &sync.Map{})
	}
	return ctx
}

func ModelAliasesFromContext(ctx context.Context) *ModelAliasPolicy {
	policy, _ := ctx.Value(modelAliasContextKey{}).(*ModelAliasPolicy)
	return policy
}

func (p *ModelAliasPolicy) IDs(model string) []string {
	return p.treeIDs(p.Canonicalize(model), make(map[string]bool))
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

// 只使用该账号的显式映射、目录或平台默认 ID 证据，规范名优先，随后按别名配置顺序选择。
// cn:/global: 原样保留在最终目标，不向账号凭据或数据库复制映射。
func accountWithModelAliases(ctx context.Context, account *Account) *Account {
	policy := ModelAliasesFromContext(ctx)
	if account == nil || policy == nil || len(policy.Groups) == 0 {
		return account
	}
	clone := *account
	clone.globalModelMapping = nil
	clone.modelAliasTreeUnavailable = make(map[string]bool)
	// 记录本次请求的别名组快照，供上游目录的同义词归一化判定使用。
	clone.modelAliasGroups = policy.Groups
	native := clone.GetModelMapping()
	overlay := make(map[string]string)
	for _, group := range policy.Groups {
		if policy.isTreeGroup(group.Canonical) {
			if target := policy.accountTreeTarget(ctx, account, group.Canonical); target != "" {
				overlay[group.Canonical] = target
			} else {
				clone.modelAliasTreeUnavailable[group.Canonical] = true
			}
			continue
		}
		for _, id := range policy.IDs(group.Canonical) {
			target, explicit := native[id]
			defaultTarget := false
			if !explicit {
				// 目录证据必须按实际 ID 精确匹配，避免把 TRAE 的大小写目标改成规范名。
				snapshot := clone.GetUpstreamSupportedModelsSnapshot()
				if snapshot != nil && !slices.Contains(snapshot.Models, id) {
					continue
				}
				if snapshot == nil {
					defaultTarget = knownDefaultAliasTarget(&clone, id)
					if !defaultTarget {
						continue
					}
				}
				target = id
			}
			if target == "" || (!defaultTarget && !clone.IsModelSupported(id)) {
				continue
			}
			if modelAliasAttempted(ctx, account, target) || clone.isRateLimitActiveForKey(modelAliasRateLimitKey(account, target)) {
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

// modelAliasGroupContains 报告 id 是否为该别名组成员（规范名或别名，忽略大小写精确匹配）。
func modelAliasGroupContains(group ModelAliasGroup, id string) bool {
	if id == "" {
		return false
	}
	if strings.EqualFold(id, group.Canonical) {
		return true
	}
	for _, alias := range group.Aliases {
		if strings.EqualFold(id, alias) {
			return true
		}
	}
	return false
}

// modelAliasCatalogMatch 报告上游目录条目 candidate 与目标 target 是否属于同一别名组。
// 只用请求级别名组做同义词互认，不剥离 provider 前缀，避免把无关模型误判为同一模型。
func (a *Account) modelAliasCatalogMatch(candidate, target string) bool {
	if a == nil || len(a.modelAliasGroups) == 0 {
		return false
	}
	candidate = strings.TrimSpace(candidate)
	target = strings.TrimSpace(target)
	if candidate == "" || target == "" {
		return false
	}
	for _, group := range a.modelAliasGroups {
		if !(&ModelAliasPolicy{Groups: a.modelAliasGroups}).isTreeGroup(group.Canonical) && modelAliasGroupContains(group, candidate) && modelAliasGroupContains(group, target) {
			return true
		}
	}
	return false
}

// modelAliasCatalogMatchAny 报告目录条目中是否存在与 target 同属一个别名组的成员。
func (a *Account) modelAliasCatalogMatchAny(catalog []string, target string) bool {
	if a == nil || len(a.modelAliasGroups) == 0 {
		return false
	}
	for _, candidate := range catalog {
		if a.modelAliasCatalogMatch(candidate, target) {
			return true
		}
	}
	return false
}

// 空映射账号仅采用该平台已公开的默认 ID，避免凭别名配置猜测上游能力。
func knownDefaultAliasTarget(account *Account, model string) bool {
	if account == nil || len(account.GetModelMapping()) != 0 {
		return false
	}
	switch account.Platform {
	case PlatformDeepseek:
		return isDeepseekServableModel(model)
	case PlatformMiniMax:
		return model == "MiniMax-M3" || model == "MiniMax-M2.7" || model == "MiniMax-M2.5"
	default:
		return false
	}
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
	out := &ModelFallbackPolicy{Enabled: policy.Enabled, AutoFallback: policy.AutoFallback, Tiers: []ModelCapabilityTier{}}
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
