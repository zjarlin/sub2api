package handler

import (
	"context"
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
	account, err := h.adminService.CreateAccount(c.Request.Context(), &service.CreateAccountInput{
		Name:                 req.Name,
		Notes:                req.Notes,
		OwnerUserID:          &subject.UserID,
		Platform:             req.Platform,
		Type:                 req.Type,
		Credentials:          req.Credentials,
		Extra:                extra,
		Concurrency:          req.Concurrency,
		LoadFactor:           req.LoadFactor,
		Priority:             req.Priority,
		RateMultiplier:       req.RateMultiplier,
		GroupIDs:             req.GroupIDs,
		SkipDefaultGroupBind: true,
		ExpiresAt:            req.ExpiresAt,
		AutoPauseOnExpired:   req.AutoPauseOnExpired,
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
	updated, err := h.adminService.UpdateAccount(c.Request.Context(), accountID, &service.UpdateAccountInput{
		Name:               req.Name,
		Notes:              req.Notes,
		Type:               req.Type,
		Credentials:        req.Credentials,
		Extra:              extra,
		Concurrency:        req.Concurrency,
		LoadFactor:         req.LoadFactor,
		Priority:           req.Priority,
		RateMultiplier:     req.RateMultiplier,
		Status:             req.Status,
		GroupIDs:           req.GroupIDs,
		ExpiresAt:          req.ExpiresAt,
		AutoPauseOnExpired: req.AutoPauseOnExpired,
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

func parseUserAccountID(c *gin.Context) (int64, bool) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return accountID, true
}
