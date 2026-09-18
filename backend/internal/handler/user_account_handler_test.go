//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ownedAccountRepoStub struct {
	owner   int64
	calls   int
	account *service.Account
}

func (s *ownedAccountRepoStub) GetByIDAndOwner(_ context.Context, id, owner int64) (*service.Account, error) {
	s.calls++
	s.owner = owner
	if id != s.account.ID || owner != *s.account.OwnerUserID {
		return nil, service.ErrAccountNotFound
	}
	return s.account, nil
}
func (s *ownedAccountRepoStub) ListByOwner(_ context.Context, owner int64, _ pagination.PaginationParams, _, _, _, _ string) ([]service.Account, *pagination.PaginationResult, error) {
	s.calls++
	s.owner = owner
	return []service.Account{*s.account}, &pagination.PaginationResult{Total: 1, Page: 1, PageSize: 20}, nil
}

type ownedAccountAdminStub struct {
	service.AdminService
	created *service.CreateAccountInput
	updated *service.UpdateAccountInput
}

func (s *ownedAccountAdminStub) CreateAccount(_ context.Context, input *service.CreateAccountInput) (*service.Account, error) {
	s.created = input
	return &service.Account{ID: 3, Name: input.Name, OwnerUserID: input.OwnerUserID, Credentials: input.Credentials, Extra: input.Extra}, nil
}
func (s *ownedAccountAdminStub) UpdateAccount(_ context.Context, id int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.updated = input
	owner := int64(11)
	return &service.Account{ID: id, OwnerUserID: &owner, Extra: input.Extra}, nil
}
func ownedAccountRequest(h *UserAccountHandler, action string, user int64, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if user > 0 {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user})
		}
		c.Next()
	})
	router.Any("/accounts/:id", map[string]gin.HandlerFunc{"get": h.GetByID, "update": h.Update, "delete": h.Delete, "test": h.Test, "create": h.Create, "list": h.List}[action])
	request := httptest.NewRequest(http.MethodPost, "/accounts/3?owner_user_id=999", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	return rec
}
func TestUserAccountOwnershipAndRedaction(t *testing.T) {
	owner := int64(11)
	repo := &ownedAccountRepoStub{account: &service.Account{ID: 3, OwnerUserID: &owner, Credentials: map[string]any{"api_key": "secret-key", "base_url": "https://example.test"}}}
	h := &UserAccountHandler{accountRepo: repo}
	for _, action := range []string{"get", "update", "delete", "test"} {
		t.Run(action, func(t *testing.T) {
			result := ownedAccountRequest(h, action, 22, `{}`)
			require.Equal(t, http.StatusNotFound, result.Code)
			require.Equal(t, int64(22), repo.owner)
		})
	}
	for _, action := range []string{"get", "list"} {
		result := ownedAccountRequest(h, action, owner, `{}`)
		require.Equal(t, http.StatusOK, result.Code)
		require.Equal(t, owner, repo.owner)
		require.NotContains(t, result.Body.String(), "secret-key")
		require.Contains(t, result.Body.String(), "https://example.test")
	}
	before := repo.calls
	result := ownedAccountRequest(h, "get", 0, `{}`)
	require.Equal(t, http.StatusUnauthorized, result.Code)
	require.Equal(t, before, repo.calls)
}
func TestUserAccountCreateIgnoresForgedOwner(t *testing.T) {
	admin := &ownedAccountAdminStub{}
	h := &UserAccountHandler{adminService: admin}
	result := ownedAccountRequest(h, "create", 11, `{"name":"owned","platform":"openai","type":"apikey","owner_user_id":999,"credentials":{"api_key":"secret-key"}}`)
	require.Equal(t, http.StatusOK, result.Code)
	require.Equal(t, int64(11), *admin.created.OwnerUserID)
	require.True(t, admin.created.SkipDefaultGroupBind)
	require.NotContains(t, result.Body.String(), "secret-key")
	require.Equal(t, false, admin.created.Extra[service.AccountPublicSharingExtraKey])
}

func TestUserAccountSharingRequiresExplicitOwnerChoice(t *testing.T) {
	owner := int64(11)
	repo := &ownedAccountRepoStub{account: &service.Account{
		ID: 3, OwnerUserID: &owner,
		Extra: map[string]any{service.AccountPublicSharingExtraKey: true, "existing": "kept"},
	}}
	admin := &ownedAccountAdminStub{}
	h := &UserAccountHandler{accountRepo: repo, adminService: admin}

	result := ownedAccountRequest(h, "update", owner, `{"shared":false}`)
	require.Equal(t, http.StatusOK, result.Code)
	require.Equal(t, false, admin.updated.Extra[service.AccountPublicSharingExtraKey])
	require.Equal(t, "kept", admin.updated.Extra["existing"])
	require.Contains(t, result.Body.String(), `"shared":false`)

	result = ownedAccountRequest(h, "update", owner, `{"extra":{"shared_for_public_scheduling":false}}`)
	require.Equal(t, http.StatusOK, result.Code)
	require.Equal(t, true, admin.updated.Extra[service.AccountPublicSharingExtraKey])

	admin.updated = nil
	result = ownedAccountRequest(h, "update", 22, `{"shared":true}`)
	require.Equal(t, http.StatusNotFound, result.Code)
	require.Nil(t, admin.updated)
}
