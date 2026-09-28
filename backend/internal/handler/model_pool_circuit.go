package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type openAIModelPoolRound struct {
	gateway        *service.OpenAIGatewayService
	groupID        *int64
	protocol       string
	model          string
	replayable     bool
	checked        bool
	blocked        bool
	permit         *service.OpenAIModelPoolPermit
	permitContext  context.Context
	candidateCount int
	candidateIDs   map[int64]struct{}
	failures       map[int64]error
	incomplete     bool
	observed       bool
}

func newOpenAIModelPoolRound(gateway *service.OpenAIGatewayService, groupID *int64, protocol, model string, replayable bool) *openAIModelPoolRound {
	return &openAIModelPoolRound{
		gateway: gateway, groupID: groupID, protocol: protocol, model: model, replayable: replayable,
	}
}

func (r *openAIModelPoolRound) reset(model string) {
	r.close()
	r.model = model
	r.checked = false
	r.blocked = false
	r.candidateCount = 0
	r.candidateIDs = nil
	r.failures = nil
	r.incomplete = false
	r.observed = false
}

func (r *openAIModelPoolRound) allowed(ctx context.Context) bool {
	if !r.replayable || r.gateway == nil {
		return true
	}
	if !r.checked {
		r.checked = true
		r.permitContext = ctx
		permit, allowed := r.gateway.AcquireOpenAIModelPool(ctx, r.groupID, r.protocol, r.model)
		r.permit = permit
		r.blocked = !allowed
	}
	return !r.blocked
}

// 仅首次无排除且明确标记完整的候选数可作池证据；不推断粘性或子池规模。
func (r *openAIModelPoolRound) selected(decision service.OpenAIAccountScheduleDecision, account *service.Account, excludedCount int) {
	if !r.replayable || !r.checked || r.candidateCount > 0 || excludedCount != 0 ||
		!decision.CandidateCountComplete || decision.CandidateCount < 2 || account == nil ||
		len(decision.CandidateAccountIDs) != decision.CandidateCount {
		return
	}
	ids := make(map[int64]struct{}, decision.CandidateCount)
	for _, id := range decision.CandidateAccountIDs {
		if id <= 0 {
			return
		}
		ids[id] = struct{}{}
	}
	if len(ids) != decision.CandidateCount {
		return
	}
	r.candidateCount = decision.CandidateCount
	r.candidateIDs = ids
}

func (r *openAIModelPoolRound) failed(accountID int64, err error) {
	if !r.replayable || accountID <= 0 || err == nil {
		return
	}
	if r.failures == nil {
		r.failures = make(map[int64]error)
	}
	r.failures[accountID] = err
}

func (r *openAIModelPoolRound) excludeWithoutUpstreamFailure() {
	r.incomplete = true
}

func (r *openAIModelPoolRound) exhausted(ctx context.Context) {
	if !r.replayable || r.gateway == nil || !r.checked || r.blocked || r.incomplete || r.observed ||
		ctx.Err() != nil || r.candidateCount < 2 || len(r.failures) != r.candidateCount {
		return
	}
	failures := make([]service.OpenAIModelPoolFailure, 0, len(r.failures))
	for accountID, err := range r.failures {
		if _, candidate := r.candidateIDs[accountID]; !candidate {
			return
		}
		failures = append(failures, service.OpenAIModelPoolFailure{AccountID: accountID, Err: err})
	}
	r.observed = true
	r.permit.Exhausted(ctx, r.candidateCount, failures)
}

func (r *openAIModelPoolRound) succeeded(ctx context.Context) {
	if r.replayable && r.gateway != nil && r.checked && !r.blocked && ctx.Err() == nil {
		r.permit.Success(ctx)
	}
}

func (r *openAIModelPoolRound) close() {
	r.permit.Release(r.permitContext)
	r.permit = nil
	r.permitContext = nil
}

func openAIModelPoolForwardSucceeded(c *gin.Context, result *service.OpenAIForwardResult, err error) bool {
	if err != nil || !openAIForwardSucceededForScheduling(result) ||
		(result != nil && result.ClientDisconnect) || !openAIRequestAllowsFailoverReplay(c) {
		return false
	}
	_, streamFailed := service.GetOpsStreamError(c)
	return !streamFailed
}
