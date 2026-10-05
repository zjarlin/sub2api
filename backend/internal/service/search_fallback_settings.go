package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const SettingKeySearchFallbackPolicy = "search_fallback_policy"

type autoModelSearchPolicyKey struct{}

// 搜索能力证据按账号和实际上游型号保存，不把供应商之间的同名模型互相背书。
type SearchProbeResult struct {
	AccountID        int64     `json:"account_id"`
	AccountName      string    `json:"account_name"`
	Model            string    `json:"model"`
	UpstreamModel    string    `json:"upstream_model"`
	ActualModel      string    `json:"actual_model,omitempty"`
	RouteFingerprint string    `json:"route_fingerprint"`
	Status           string    `json:"status"`
	CheckedAt        time.Time `json:"checked_at"`
	SourceURLs       []string  `json:"source_urls"`
	Message          string    `json:"message,omitempty"`
}

type SearchFallbackPolicy struct {
	Enabled                 bool                `json:"enabled"`
	Models                  []string            `json:"models"`
	RequireVerified         bool                `json:"require_verified"`
	CandidateTimeoutSeconds int                 `json:"candidate_timeout_seconds"`
	TimeoutSeconds          int                 `json:"timeout_seconds"`
	ProbeResults            []SearchProbeResult `json:"probe_results"`
}

func DefaultSearchFallbackPolicy() *SearchFallbackPolicy {
	return &SearchFallbackPolicy{Enabled: true, Models: []string{}, CandidateTimeoutSeconds: 45, TimeoutSeconds: 120, ProbeResults: []SearchProbeResult{}}
}

func (p *SearchFallbackPolicy) Validate() error {
	if p == nil || len(p.Models) > 256 || len(p.ProbeResults) > 2048 {
		return fmt.Errorf("search policy allows at most 256 models and 2048 probe results")
	}
	seen := map[string]bool{}
	for _, model := range p.Models {
		if model == "" || len(model) > 200 || strings.ContainsAny(model, " \t\n\r*") || seen[model] {
			return fmt.Errorf("search model IDs must be unique, nonempty and exact")
		}
		seen[model] = true
	}
	if p.CandidateTimeoutSeconds < 1 || p.CandidateTimeoutSeconds > 120 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 300 {
		return fmt.Errorf("search candidate timeout must be 1-120 seconds and total timeout 1-300 seconds")
	}
	for _, result := range p.ProbeResults {
		if result.AccountID <= 0 || result.Model == "" || result.UpstreamModel == "" || result.CheckedAt.IsZero() || !slices.Contains([]string{"supported", "unverified", "busy"}, result.Status) {
			return fmt.Errorf("invalid search probe evidence")
		}
		if result.Status == "supported" && len(result.SourceURLs) == 0 {
			return fmt.Errorf("verified search requires source URLs")
		}
	}
	return nil
}

func (s *SettingService) GetSearchFallbackPolicy(ctx context.Context) (*SearchFallbackPolicy, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultSearchFallbackPolicy(), nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeySearchFallbackPolicy)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		return DefaultSearchFallbackPolicy(), nil
	}
	if err != nil {
		return nil, err
	}
	var policy SearchFallbackPolicy
	if err = json.Unmarshal([]byte(raw), &policy); err != nil {
		return nil, fmt.Errorf("decode search fallback policy: %w", err)
	}
	if err = policy.Validate(); err != nil {
		return nil, err
	}
	if policy.Models == nil {
		policy.Models = []string{}
	}
	if policy.ProbeResults == nil {
		policy.ProbeResults = []SearchProbeResult{}
	}
	return &policy, nil
}

func (s *SettingService) SetSearchFallbackPolicy(ctx context.Context, policy *SearchFallbackPolicy) error {
	s.searchProbeMu.Lock()
	defer s.searchProbeMu.Unlock()
	current, err := s.GetSearchFallbackPolicy(ctx)
	if err != nil {
		return err
	}
	policy.ProbeResults = current.ProbeResults
	return s.saveSearchFallbackPolicy(ctx, policy)
}

func (s *SettingService) saveSearchFallbackPolicy(ctx context.Context, policy *SearchFallbackPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeySearchFallbackPolicy, string(data))
}

// 单条探查立即持久化，批次中断后保留已经完成的证据。
func (s *SettingService) SaveSearchProbeResult(ctx context.Context, result SearchProbeResult) error {
	s.searchProbeMu.Lock()
	defer s.searchProbeMu.Unlock()
	policy, err := s.GetSearchFallbackPolicy(ctx)
	if err != nil {
		return err
	}
	policy.ProbeResults = slices.DeleteFunc(policy.ProbeResults, func(previous SearchProbeResult) bool {
		return previous.AccountID == result.AccountID && previous.Model == result.Model
	})
	policy.ProbeResults = append(policy.ProbeResults, result)
	return s.saveSearchFallbackPolicy(ctx, policy)
}

func configuredSearchFallbackCandidates(ctx context.Context, accounts []Account, group *Group, body []byte, policy *SearchFallbackPolicy) []searchFallbackCandidate {
	if !policy.Enabled {
		return nil
	}
	candidates := searchFallbackCandidates(ctx, accounts, group, body)
	aliases := ModelAliasesFromContext(ctx)
	candidates = slices.DeleteFunc(candidates, func(candidate searchFallbackCandidate) bool {
		if !policy.RequireVerified {
			return false
		}
		target := ResolveOpenAIAccountUpstreamModelForRequest(candidate.account, candidate.model, false)
		for _, result := range policy.ProbeResults {
			if result.AccountID == candidate.account.ID && result.RouteFingerprint == searchProbeRouteFingerprint(candidate.account, target) && result.UpstreamModel == target && aliases.Canonicalize(result.Model) == aliases.Canonicalize(candidate.model) {
				return result.Status != "supported" || !AutoModelAllowed(ctx, result.ActualModel)
			}
		}
		return true
	})
	// 已配置型号的偏好先于默认 Auto 顺序，账号资格与成本边界仍由原筛选保证。
	slices.SortStableFunc(candidates, func(left, right searchFallbackCandidate) int {
		models := aliases.CanonicalIDs(policy.Models)
		li, ri := slices.Index(models, aliases.Canonicalize(left.model)), slices.Index(models, aliases.Canonicalize(right.model))
		if li < 0 {
			li = len(models)
		}
		if ri < 0 {
			ri = len(models)
		}
		return li - ri
	})
	return candidates
}

func searchProbeRouteFingerprint(account *Account, upstream string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(account.Platform+"|"+account.Type+"|"+account.GetOpenAIBaseURL()+"|"+upstream)))
}
