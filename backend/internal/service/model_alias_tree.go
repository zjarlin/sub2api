package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// 路由模型保留精确上游 ID；优先级越小越先尝试，地址按完整基础路径精确匹配。
type ModelAliasRoute struct {
	Model    string   `json:"model"`
	BaseURLs []string `json:"base_urls,omitempty"`
	Priority int      `json:"priority"`
}

func normalizedModelBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func (p *ModelAliasPolicy) group(model string) *ModelAliasGroup {
	if p == nil {
		return nil
	}
	for i := range p.Groups {
		if p.Groups[i].Canonical == model {
			return &p.Groups[i]
		}
	}
	return nil
}

func (p *ModelAliasPolicy) validateTree() error {
	for _, g := range p.Groups {
		if g.Parent != "" && p.group(g.Parent) == nil {
			return fmt.Errorf("unknown model parent %q", g.Parent)
		}
		seen := map[string]bool{g.Canonical: true}
		for parent := g.Parent; parent != ""; {
			if seen[parent] {
				return fmt.Errorf("model tree cycle at %q", parent)
			}
			seen[parent] = true
			node := p.group(parent)
			if node == nil {
				return fmt.Errorf("unknown model parent %q", parent)
			}
			parent = node.Parent
		}
		for _, route := range g.Routes {
			if route.Model != g.Canonical && !slices.Contains(g.Aliases, route.Model) {
				return fmt.Errorf("route %q must belong to %q", route.Model, g.Canonical)
			}
			for _, base := range route.BaseURLs {
				u, err := url.Parse(base)
				if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
					return fmt.Errorf("invalid model base URL %q", base)
				}
			}
		}
	}
	return nil
}

// 对外目录只投影根节点；内部规范名保留子节点，避免把不同型号误当成同一模型。
func (p *ModelAliasPolicy) RootID(model string) string {
	model = p.Canonicalize(model)
	for steps := 0; p != nil && steps < len(p.Groups); steps++ {
		g := p.group(model)
		if g == nil || g.Parent == "" {
			break
		}
		model = g.Parent
	}
	return model
}

func (p *ModelAliasPolicy) isTreeGroup(model string) bool {
	g := p.group(model)
	if g == nil {
		return false
	}
	if g.Parent != "" || len(g.Routes) > 0 {
		return true
	}
	for _, child := range p.Groups {
		if child.Parent == model {
			return true
		}
	}
	return false
}

func (p *ModelAliasPolicy) treeIDs(model string, seen map[string]bool) []string {
	if seen[model] {
		return nil
	}
	seen[model] = true
	g := p.group(model)
	if g == nil {
		return []string{model}
	}
	out := []string{}
	for _, child := range p.Groups {
		if child.Parent == model {
			out = append(out, p.treeIDs(child.Canonical, seen)...)
		}
	}
	routes := slices.Clone(g.Routes)
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].Priority < routes[j].Priority })
	for _, route := range routes {
		out = append(out, route.Model)
	}
	out = append(out, g.Canonical)
	out = append(out, g.Aliases...)
	unique := []string{}
	used := map[string]bool{}
	for _, id := range out {
		if !used[id] {
			unique = append(unique, id)
			used[id] = true
		}
	}
	return unique
}

// 探测证据绑定账号与基础地址；429、认证失败和超时均不能作为永久能力否定。
const ModelRouteProbesExtraKey = "model_route_probes"

type ModelRouteProbeSnapshot struct {
	BaseURL   string            `json:"base_url"`
	CheckedAt time.Time         `json:"checked_at"`
	Models    map[string]string `json:"models"`
}

func (a *Account) modelRouteProbe(model string) string {
	if a == nil || a.Extra == nil {
		return ""
	}
	raw, err := json.Marshal(a.Extra[ModelRouteProbesExtraKey])
	if err != nil {
		return ""
	}
	var snapshot ModelRouteProbeSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || normalizedModelBaseURL(snapshot.BaseURL) != normalizedModelBaseURL(a.GetOpenAIBaseURL()) || snapshot.CheckedAt.After(time.Now().Add(5*time.Minute)) {
		return ""
	}
	status := snapshot.Models[model]
	if status != "supported" && time.Since(snapshot.CheckedAt) > 12*time.Hour {
		return ""
	}
	return status
}

func modelAliasRateLimitKey(a *Account, model string) string {
	sum := sha256.Sum256([]byte(normalizedModelBaseURL(a.GetOpenAIBaseURL())))
	return fmt.Sprintf("model-route:%x:%s", sum[:8], model)
}

type modelAliasAttemptsKey struct{}

func modelAliasAttemptKey(a *Account, model string) string {
	return fmt.Sprintf("%d:%s", a.ID, modelAliasRateLimitKey(a, model))
}
func modelAliasAttempted(ctx context.Context, a *Account, model string) bool {
	attempts, _ := ctx.Value(modelAliasAttemptsKey{}).(*sync.Map)
	if attempts == nil {
		return false
	}
	_, found := attempts.Load(modelAliasAttemptKey(a, model))
	return found
}

func (p *ModelAliasPolicy) routeAllows(a *Account, group *ModelAliasGroup, model string) bool {
	constrained := false
	for _, r := range group.Routes {
		if r.Model != model {
			continue
		}
		constrained = true
		if len(r.BaseURLs) == 0 {
			return true
		}
		for _, base := range r.BaseURLs {
			if normalizedModelBaseURL(base) == normalizedModelBaseURL(a.GetOpenAIBaseURL()) {
				return true
			}
		}
	}
	return !constrained
}

func (p *ModelAliasPolicy) accountTreeTarget(ctx context.Context, a *Account, model string) string {
	// 只从原生白名单、精确目录或实测成功中选择，禁止借同义词推断另一个端点的能力。
	native := a.getNativeModelMapping()
	for _, id := range p.IDs(model) {
		owner := p.group(p.Canonicalize(id))
		if owner == nil || !p.routeAllows(a, owner, id) {
			continue
		}
		target, explicit := native[id]
		if !explicit {
			target = id
		}
		if targetOwner := p.group(p.Canonicalize(target)); targetOwner != nil && !p.routeAllows(a, targetOwner, target) {
			continue
		}
		if target == "" || modelAliasAttempted(ctx, a, target) || a.isRateLimitActiveForKey(target) || a.isRateLimitActiveForKey(modelAliasRateLimitKey(a, target)) {
			continue
		}
		probe := a.modelRouteProbe(target)
		if probe == "unsupported" {
			continue
		}
		if probe != "supported" && a.IsModelKnownUnsupported(target) {
			continue
		}
		evidenced := probe == "supported"
		if snapshot := a.GetUpstreamSupportedModelsSnapshot(); snapshot != nil && slices.Contains(snapshot.Models, target) {
			evidenced = true
		}
		if !explicit && len(native) > 0 {
			// 仅当账号原已允许此子组时，成功探测才能补齐其新的同义上游 ID。
			allowed := false
			for _, member := range append([]string{owner.Canonical}, owner.Aliases...) {
				if _, ok := native[member]; ok {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		if !evidenced && !explicit && !knownDefaultAliasTarget(a, target) {
			continue
		}
		return target
	}
	return ""
}

// 请求内只遍历每个账号/地址/叶子一次；重新选号时派生映射会跳过已经失败的叶子。
func TryNextModelAlias(ctx context.Context, a *Account, model string, status int) bool {
	if a == nil || (status != 429 && status != 404 && status != 410 && status != 503) {
		return false
	}
	p := ModelAliasesFromContext(ctx)
	model = p.Canonicalize(model)
	if !p.isTreeGroup(model) {
		return false
	}
	target := ResolveOpenAIAccountUpstreamModelForRequest(a, model, false)
	attempts, _ := ctx.Value(modelAliasAttemptsKey{}).(*sync.Map)
	if attempts == nil {
		return false
	}
	attempts.Store(modelAliasAttemptKey(a, target), true)
	next := p.accountTreeTarget(ctx, a, model)
	return next != "" && next != target
}

// 跨账号也遵循叶子顺序；账号自身的权重与负载只在同一叶子内部排序。
func (p *ModelAliasPolicy) AccountRouteRank(a *Account, model string) int {
	if !p.isTreeGroup(p.Canonicalize(model)) || a == nil {
		return 0
	}
	target := ResolveOpenAIAccountUpstreamModelForRequest(a, model, false)
	for rank, id := range p.IDs(model) {
		if id == target {
			return rank
		}
	}
	return 4096
}

// 指定子型号耗尽后先遍历同父兄弟，再向上遍历；兄弟子树之间不重复访问。
func (p *ModelAliasPolicy) TreeFallbackCandidates(model string) []ModelFallbackCandidate {
	model = p.Canonicalize(model)
	seen := map[string]bool{model: true}
	var out []ModelFallbackCandidate
	for node := p.group(model); node != nil && node.Parent != ""; node = p.group(node.Parent) {
		seen[node.Canonical] = true
		for _, g := range p.Groups {
			if g.Parent == node.Parent && !seen[g.Canonical] {
				seen[g.Canonical] = true
				out = append(out, ModelFallbackCandidate{Model: g.Canonical, Tier: node.Parent})
			}
		}
		seen[node.Parent] = true
	}
	return out
}

// 自定义树入口按其首个可识别子模型选协议，仍沿用既有模型平台检测规则。
func DetectModelPlatformWithAliases(ctx context.Context, model string) (string, bool) {
	if platform, ok := DetectModelPlatform(model); ok {
		return platform, true
	}
	p := ModelAliasesFromContext(ctx)
	if !p.isTreeGroup(model) {
		return "", false
	}
	for _, id := range p.IDs(model) {
		if platform, ok := DetectModelPlatform(id); ok {
			return platform, true
		}
	}
	return "", false
}
