package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

type SearchProbeCandidate struct {
	AccountID     int64  `json:"account_id"`
	AccountName   string `json:"account_name"`
	Model         string `json:"model"`
	UpstreamModel string `json:"upstream_model"`
}

// 探查独立于已验证名单和 Auto 排序，不把当前未知的渠道永远排除在发现流程之外。
func (s *OpenAIGatewayService) SearchProbeCandidates(ctx context.Context, groupID int64) ([]SearchProbeCandidate, error) {
	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if s.settingService != nil {
		aliases, aliasErr := s.settingService.GetModelAliasPolicy(ctx)
		if aliasErr != nil {
			return nil, aliasErr
		}
		ctx = WithModelAliases(ctx, aliases)
	}
	accounts = accountsWithModelAliases(ctx, accounts)
	body := []byte(`{"tools":[{"type":"web_search"}]}`)
	output := []SearchProbeCandidate{}
	for _, candidate := range searchFallbackCandidates(ctx, accounts, nil, body) {
		output = append(output, SearchProbeCandidate{candidate.account.ID, candidate.account.Name, candidate.model, ResolveOpenAIAccountUpstreamModelForRequest(candidate.account, candidate.model, false)})
	}
	return output, nil
}

// 复用真实网关转发链探查，包含渠道映射、代理、认证和原生 Responses 行为。
func (s *OpenAIGatewayService) ProbeSearchCapability(ctx context.Context, parent *gin.Context, groupID, accountID int64, model string) (*SearchProbeResult, error) {
	candidates, err := s.SearchProbeCandidates(ctx, groupID)
	if err != nil {
		return nil, err
	}
	var selected *SearchProbeCandidate
	for i := range candidates {
		if candidates[i].AccountID == accountID && candidates[i].Model == model {
			selected = &candidates[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("account/model is not a native search candidate in this group")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if s.settingService != nil {
		aliases, aliasErr := s.settingService.GetModelAliasPolicy(ctx)
		if aliasErr != nil {
			return nil, aliasErr
		}
		ctx = WithModelAliases(ctx, aliases)
		account = accountWithModelAliases(ctx, account)
	}
	result := &SearchProbeResult{AccountID: accountID, AccountName: account.Name, Model: model, UpstreamModel: selected.UpstreamModel, RouteFingerprint: searchProbeRouteFingerprint(account, selected.UpstreamModel), Status: "unverified", CheckedAt: time.Now().UTC(), SourceURLs: []string{}}
	release := func() {}
	if s.concurrencyService != nil {
		slot, slotErr := s.concurrencyService.AcquireAccountSlot(ctx, accountID, account.Concurrency)
		if slotErr != nil {
			return nil, slotErr
		}
		if !slot.Acquired {
			result.Status, result.Message = "busy", "Account concurrency capacity is occupied; capability is unconfirmed"
			return result, s.settingService.SaveSearchProbeResult(ctx, *result)
		}
		release = slot.ReleaseFunc
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	key := &APIKey{GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformOpenAI}}
	body := []byte(`{"tools":[{"type":"web_search"}]}`)
	probeContext := parent.Copy()
	probeContext.Request = parent.Request.Clone(ctx)
	query := "Search the web now for the official Go release notes. Cite the official source URL and summarize one fact. Perform a real search, not a response from memory."
	_, callErr := s.callSearchFallbackHelper(ctx, probeContext, key, searchFallbackCandidate{account: account, model: model}, body, query)
	if callErr == nil {
		result.Status = "supported"
		if sources, ok := probeContext.Get("search_helper_source_urls"); ok {
			result.SourceURLs, _ = sources.([]string)
		}
		for _, usage := range TakeVisionFallbackUsage(probeContext) {
			result.ActualModel = usage.Result.UpstreamResponseModel
			if result.ActualModel == "" {
				result.ActualModel = usage.Result.Model
			}
		}
	} else {
		result.Message = "No completed web search with structured source citations"
		var failure *UpstreamFailoverError
		if errors.As(callErr, &failure) && failure.StatusCode >= 400 {
			result.Message = fmt.Sprintf("Search probe failed (HTTP %d); capability is unconfirmed", failure.StatusCode)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Message = "Search probe timed out; capability is unconfirmed"
		}
	}
	if len(result.SourceURLs) == 0 && result.Status == "supported" {
		result.Status = "unverified"
	}
	// 使用父请求上下文持久化超时结果，单模型超时不能取消整个探查批次。
	if err = s.settingService.SaveSearchProbeResult(parent.Request.Context(), *result); err != nil {
		return nil, err
	}
	return result, nil
}
