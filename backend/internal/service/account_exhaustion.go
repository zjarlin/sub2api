package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const AccountExhaustionExtraKey = "exhaustion_policy"

// 榨干策略只记录管理员意图；额度、冷却和并发仍由原调度器管理。
type AccountExhaustionPolicy struct {
	Enabled  bool     `json:"enabled"`
	Models   []string `json:"models"`
	GroupIDs []int64  `json:"group_ids"`
}

func readAccountExhaustionPolicy(extra map[string]any) (AccountExhaustionPolicy, error) {
	var policy AccountExhaustionPolicy
	raw, exists := extra[AccountExhaustionExtraKey]
	if !exists || raw == nil {
		return policy, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return policy, err
	}
	err = json.Unmarshal(data, &policy)
	return policy, err
}

func (a *Account) ExhaustionPolicy() AccountExhaustionPolicy {
	if a == nil {
		return AccountExhaustionPolicy{}
	}
	policy, err := readAccountExhaustionPolicy(a.Extra)
	if err != nil || len(policy.Models) == 0 || len(policy.Models) > 16 {
		return AccountExhaustionPolicy{}
	}
	return policy
}

func (p AccountExhaustionPolicy) applies(groupID *int64) bool {
	return p.Enabled && (len(p.GroupIDs) == 0 || (groupID != nil && slices.Contains(p.GroupIDs, *groupID)))
}

// 保存时检查最终账号状态，防止模型映射或分组变更留下越权配置。
func ValidateAccountExhaustionPolicy(a *Account, groupIDs []int64) error {
	p, err := readAccountExhaustionPolicy(a.Extra)
	bad := func(message string) error { return infraerrors.BadRequest("INVALID_EXHAUSTION_POLICY", message) }
	if err != nil {
		return bad("invalid exhaustion policy")
	}
	if !p.Enabled {
		return nil
	}
	if !a.IsOpenAICompatible() {
		return bad("exhaustion mode requires an OpenAI-compatible account")
	}
	if len(p.Models) == 0 || len(p.Models) > 16 {
		return bad("exhaustion mode requires 1 to 16 models")
	}
	seen := make(map[string]bool)
	for _, model := range p.Models {
		if model == "" || strings.TrimSpace(model) != model || len(model) > 200 || !AutoModelTextCandidate(model) || seen[model] {
			return bad("exhaustion models must be unique text model IDs")
		}
		if !a.IsModelSupported(model) {
			return bad(fmt.Sprintf("exhaustion model %q is not supported by this account", model))
		}
		seen[model] = true
	}
	for _, id := range p.GroupIDs {
		if id <= 0 || !slices.Contains(groupIDs, id) {
			return bad("exhaustion groups must be bound to the account")
		}
	}
	return nil
}

// Auto 从同一份请求快照构建账号顺序，避免重读数据库或改变普通候选池。
func AutoModelExhaustionAccounts(ctx context.Context, groupID int64) []Account {
	snapshot, ok := ctx.Value(autoModelAccountsKey{}).(*autoModelInventory)
	if !ok || snapshot.groupID != groupID {
		return nil
	}
	accounts := make([]Account, 0)
	for _, a := range snapshot.accounts {
		if a.IsOpenAICompatible() && a.IsSchedulable() && a.ExhaustionPolicy().applies(&groupID) {
			accounts = append(accounts, a)
		}
	}
	sortExhaustionAccounts(accounts)
	return accounts
}

func sortExhaustionAccounts(accounts []Account) {
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].Priority != accounts[j].Priority {
			return accounts[i].Priority < accounts[j].Priority
		}
		return accounts[i].ID < accounts[j].ID
	})
}

type exhaustionRouteKey struct{}
type exhaustionAcquireKey struct{}
type exhaustionAttempt struct {
	accountID int64
	model     string
}
type exhaustionRequestState struct {
	mu    sync.Mutex
	tried map[exhaustionAttempt]bool
	busy  map[int64]bool
}
type exhaustionRoute struct {
	accountID int64
	model     string
	state     *exhaustionRequestState
}

// 每次换候选记录上一次账号和模型；普通池仍保留同模型的其他账号。
func WithAccountExhaustionRoute(ctx context.Context, accountID int64, model string) context.Context {
	old, _ := ctx.Value(exhaustionRouteKey{}).(exhaustionRoute)
	state := old.state
	if state == nil {
		state = &exhaustionRequestState{tried: make(map[exhaustionAttempt]bool), busy: make(map[int64]bool)}
	}
	if old.accountID > 0 {
		state.mu.Lock()
		state.tried[exhaustionAttempt{old.accountID, old.model}] = true
		state.mu.Unlock()
	}
	return context.WithValue(ctx, exhaustionRouteKey{}, exhaustionRoute{accountID, model, state})
}

func AccountExhaustionTarget(ctx context.Context) int64 {
	route, _ := ctx.Value(exhaustionRouteKey{}).(exhaustionRoute)
	return route.accountID
}

func exhaustionExcludedAccounts(ctx context.Context, model string, excluded map[int64]struct{}) map[int64]struct{} {
	route, _ := ctx.Value(exhaustionRouteKey{}).(exhaustionRoute)
	if route.state == nil {
		return excluded
	}
	result := cloneExcludedAccountIDs(excluded)
	if result == nil {
		result = make(map[int64]struct{})
	}
	route.state.mu.Lock()
	defer route.state.mu.Unlock()
	for attempt := range route.state.tried {
		if attempt.model == model {
			result[attempt.accountID] = struct{}{}
		}
	}
	for id := range route.state.busy {
		result[id] = struct{}{}
	}
	return result
}

func exhaustionMatchesModel(ctx context.Context, a *Account, model string) bool {
	aliases := ModelAliasesFromContext(ctx)
	for _, selected := range a.ExhaustionPolicy().Models {
		if selected == model || (aliases != nil && aliases.Canonicalize(selected) == aliases.Canonicalize(model)) ||
			ResolveOpenAIAccountUpstreamModelForRequest(a, selected, false) == model {
			return true
		}
	}
	return false
}

// 指定候选只抢该账号；普通请求先尝试开启榨干的账号，再交回原调度链路。
func (s *OpenAIGatewayService) selectExhaustionAccount(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, bool, error) {
	target := AccountExhaustionTarget(ctx)
	decision := OpenAIAccountScheduleDecision{Layer: "account_exhaustion"}
	// 原生续接与任务父账号的强绑定不可迁移。
	if req.PreviousResponseID != "" || req.GuardianParentAccountID > 0 || req.RequiredImageCapability != "" {
		return nil, decision, false, nil
	}
	if s.checkChannelPricingRestriction(ctx, req.GroupID, req.RequestedModel) {
		return nil, decision, target > 0, ErrNoAvailableAccounts
	}
	if s.accountRepo == nil && s.schedulerSnapshot == nil {
		if target > 0 {
			return nil, decision, true, ErrNoAvailableAccounts
		}
		return nil, decision, false, nil
	}
	accounts, err := s.listSchedulableAccounts(ctx, req.GroupID, req.Platform)
	if err != nil {
		return nil, decision, true, err
	}
	sortExhaustionAccounts(accounts)
	scheduler := &defaultOpenAIAccountScheduler{service: s, stats: newOpenAIAccountRuntimeStats()}
	for i := range accounts {
		a := &accounts[i]
		if (target > 0 && a.ID != target) || openAIAccountExcluded(req.ExcludedIDs, a.ID) ||
			!a.ExhaustionPolicy().applies(req.GroupID) || !exhaustionMatchesModel(ctx, a, req.RequestedModel) {
			continue
		}
		if !a.IsSchedulable() || !a.IsOpenAICompatible() || !s.openAIAccountMatchesSchedulingGroup(a, req.GroupID) ||
			!openAIAccountMatchesPlatform(a, req.Platform) || !scheduler.isAccountRequestCompatible(ctx, a, req) ||
			!scheduler.isAccountTransportCompatible(a, req.RequiredTransport) {
			continue
		}
		upstreamModel := canonicalOpenAIAccountSchedulingModel(a, req.RequestedModel)
		if isGrokTeamModelRateLimited(a, upstreamModel, time.Now()) || isGrokModelQuotaBlocked(a.ID, upstreamModel, time.Now()) {
			continue
		}
		selected, _, err := scheduler.tryAcquireOpenAISelectionOrder(context.WithValue(ctx, exhaustionAcquireKey{}, true), req, []openAIAccountCandidateScore{{account: a}})
		if err != nil {
			return nil, decision, true, err
		}
		if selected == nil {
			continue
		}
		// 抢槽后的新鲜账号也必须仍启用此策略，关闭开关立即停止优先调度。
		if !selected.Account.ExhaustionPolicy().applies(req.GroupID) || !exhaustionMatchesModel(ctx, selected.Account, req.RequestedModel) {
			if selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
			continue
		}
		decision.SelectedAccountID = selected.Account.ID
		decision.SelectedAccountType = selected.Account.Type
		return selected, decision, true, nil
	}
	if target > 0 {
		return nil, decision, true, ErrNoAvailableAccounts
	}
	return nil, decision, false, nil
}

func markExhaustionAccountBusy(ctx context.Context, id int64) {
	if checking, _ := ctx.Value(exhaustionAcquireKey{}).(bool); !checking {
		return
	}
	route, _ := ctx.Value(exhaustionRouteKey{}).(exhaustionRoute)
	if route.state == nil {
		return
	}
	route.state.mu.Lock()
	route.state.busy[id] = true
	route.state.mu.Unlock()
}
