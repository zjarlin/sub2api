package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type UserAccountHandler struct {
	adminService       service.AdminService
	accountRepo        userAccountRepository
	accountTestService *service.AccountTestService
}

type userAccountRepository interface {
	GetByIDAndOwner(ctx context.Context, id, ownerUserID int64) (*service.Account, error)
	ListByOwner(ctx context.Context, ownerUserID int64, params pagination.PaginationParams, platform, accountType, status, search string) ([]service.Account, *pagination.PaginationResult, error)
}

func NewUserAccountHandler(
	adminService service.AdminService,
	accountRepo service.AccountRepository,
	accountTestService *service.AccountTestService,
) *UserAccountHandler {
	userRepo, ok := accountRepo.(userAccountRepository)
	if !ok {
		panic("account repository does not support user-owned accounts")
	}
	return &UserAccountHandler{
		adminService:       adminService,
		accountRepo:        userRepo,
		accountTestService: accountTestService,
	}
}

func (h *UserAccountHandler) List(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "name"),
		SortOrder: c.DefaultQuery("sort_order", "asc"),
	}
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 100 {
		search = search[:100]
	}

	accounts, result, err := h.accountRepo.ListByOwner(
		c.Request.Context(),
		subject.UserID,
		params,
		strings.TrimSpace(c.Query("platform")),
		strings.TrimSpace(c.Query("type")),
		strings.TrimSpace(c.Query("status")),
		search,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	items := make([]*dto.Account, 0, len(accounts))
	for i := range accounts {
		items = append(items, dto.AccountFromService(&accounts[i]))
	}
	response.Paginated(c, items, result.Total, result.Page, result.PageSize)
}

func (h *UserAccountHandler) GetByID(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	accountID, ok := parseUserAccountID(c)
	if !ok {
		return
	}

	account, err := h.accountRepo.GetByIDAndOwner(c.Request.Context(), accountID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AccountFromService(account))
}

func (h *UserAccountHandler) Create(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	var req userCreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	extra := ownedAccountExtra(req.Extra, req.Shared)
	skipCheck := req.ConfirmMixedChannelRisk != nil && *req.ConfirmMixedChannelRisk
	account, err := h.adminService.CreateAccount(c.Request.Context(), &service.CreateAccountInput{
		Name:                  req.Name,
		Notes:                 req.Notes,
		OwnerUserID:           &subject.UserID,
		Platform:              req.Platform,
		Type:                  req.Type,
		Credentials:           req.Credentials,
		Extra:                 extra,
		ProxyID:               req.ProxyID,
		Concurrency:           req.Concurrency,
		LoadFactor:            req.LoadFactor,
		Priority:              req.Priority,
		RateMultiplier:        req.RateMultiplier,
		GroupIDs:              req.GroupIDs,
		ExpiresAt:             req.ExpiresAt,
		AutoPauseOnExpired:    req.AutoPauseOnExpired,
		ProbeEnabled:          req.ProbeEnabled,
		SkipMixedChannelCheck: skipCheck,
	})
	if err != nil {
		if retryAfter := service.RetryAfterSecondsFromError(err); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AccountFromService(account))
}

func (h *UserAccountHandler) Update(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	accountID, ok := parseUserAccountID(c)
	if !ok {
		return
	}
	account, err := h.accountRepo.GetByIDAndOwner(c.Request.Context(), accountID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	var req userUpdateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	var extra map[string]any
	if req.Extra != nil || req.Shared != nil {
		shared := account.IsPubliclyShared()
		if req.Shared != nil {
			shared = *req.Shared
		}
		if req.Extra != nil {
			extra = ownedAccountExtra(req.Extra, shared)
		} else {
			extra = ownedAccountExtra(account.Extra, shared)
		}
	}
	skipCheck := req.ConfirmMixedChannelRisk != nil && *req.ConfirmMixedChannelRisk
	updated, err := h.adminService.UpdateAccount(c.Request.Context(), accountID, &service.UpdateAccountInput{
		Name:                  req.Name,
		Notes:                 req.Notes,
		Type:                  req.Type,
		Credentials:           req.Credentials,
		Extra:                 extra,
		ProxyID:               req.ProxyID,
		Concurrency:           req.Concurrency,
		LoadFactor:            req.LoadFactor,
		Priority:              req.Priority,
		RateMultiplier:        req.RateMultiplier,
		Status:                req.Status,
		GroupIDs:              req.GroupIDs,
		ExpiresAt:             req.ExpiresAt,
		AutoPauseOnExpired:    req.AutoPauseOnExpired,
		ProbeEnabled:          req.ProbeEnabled,
		RateSyncEnabled:       req.RateSyncEnabled,
		SkipMixedChannelCheck: skipCheck,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AccountFromService(updated))
}

func ownedAccountExtra(source map[string]any, shared bool) map[string]any {
	extra := make(map[string]any, len(source)+1)
	for key, value := range source {
		extra[key] = value
	}
	extra[service.AccountPublicSharingExtraKey] = shared
	return extra
}

func (h *UserAccountHandler) Delete(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	accountID, ok := parseUserAccountID(c)
	if !ok {
		return
	}
	if _, err := h.accountRepo.GetByIDAndOwner(c.Request.Context(), accountID, subject.UserID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.adminService.DeleteAccount(c.Request.Context(), accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Account deleted successfully"})
}

func (h *UserAccountHandler) Test(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	accountID, ok := parseUserAccountID(c)
	if !ok {
		return
	}
	if _, err := h.accountRepo.GetByIDAndOwner(c.Request.Context(), accountID, subject.UserID); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	var req userTestAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.accountTestService.TestAccountConnection(c, accountID, req.ModelID, req.Prompt, req.Mode); err != nil {
		return
	}
}

// CheckMixedChannel checks whether binding the current user's account to the selected groups mixes platforms.
func (h *UserAccountHandler) CheckMixedChannel(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	var req struct {
		Platform  string  `json:"platform" binding:"required"`
		GroupIDs  []int64 `json:"group_ids"`
		AccountID *int64  `json:"account_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.GroupIDs) == 0 {
		response.Success(c, gin.H{"has_risk": false})
		return
	}

	accountID := int64(0)
	if req.AccountID != nil {
		if *req.AccountID <= 0 {
			response.BadRequest(c, "Invalid account ID")
			return
		}
		if _, err := h.accountRepo.GetByIDAndOwner(c.Request.Context(), *req.AccountID, subject.UserID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
		accountID = *req.AccountID
	}

	err := h.adminService.CheckMixedChannelRisk(c.Request.Context(), accountID, req.Platform, req.GroupIDs)
	if err != nil {
		var mixedErr *service.MixedChannelError
		if errors.As(err, &mixedErr) {
			response.Success(c, gin.H{
				"has_risk": true,
				"error":    "mixed_channel_warning",
				"message":  mixedErr.Error(),
				"details": gin.H{
					"group_id":         mixedErr.GroupID,
					"group_name":       mixedErr.GroupName,
					"current_platform": mixedErr.CurrentPlatform,
					"other_platform":   mixedErr.OtherPlatform,
				},
			})
			return
		}
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"has_risk": false})
}

// SyncUpstreamModels refreshes the model catalog for one of the current user's own accounts.
func (h *UserAccountHandler) SyncUpstreamModels(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	accountID, ok := parseUserAccountID(c)
	if !ok {
		return
	}
	account, err := h.accountRepo.GetByIDAndOwner(c.Request.Context(), accountID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.accountTestService == nil {
		response.InternalError(c, "Account test service is not configured")
		return
	}

	catalog, err := h.accountTestService.SyncUpstreamModelCatalog(c.Request.Context(), account)
	if err != nil {
		var syncErr *service.UpstreamModelSyncError
		if errors.As(err, &syncErr) {
			switch syncErr.Kind {
			case service.UpstreamModelSyncErrorConfiguration, service.UpstreamModelSyncErrorUnsupported:
				response.BadRequest(c, syncErr.SafeMessage())
			case service.UpstreamModelSyncErrorInternal:
				response.InternalError(c, syncErr.SafeMessage())
			default:
				response.Error(c, http.StatusBadGateway, syncErr.SafeMessage())
			}
			return
		}
		response.Error(c, http.StatusBadGateway, "Failed to sync upstream models from upstream")
		return
	}

	response.Success(c, catalog)
}

func parseUserAccountID(c *gin.Context) (int64, bool) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return accountID, true
}
