//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type codexCatalogAccountRepoStub struct {
	accountRepoStub
	groupCalls    []codexCatalogGroupCall
	platformCalls []string
	groupAccounts []Account
	allAccounts   []Account
}

type codexCatalogGroupCall struct {
	groupID  int64
	platform string
}

func (s *codexCatalogAccountRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error) {
	s.groupCalls = append(s.groupCalls, codexCatalogGroupCall{groupID: groupID, platform: platform})
	return append([]Account(nil), s.groupAccounts...), nil
}

func (s *codexCatalogAccountRepoStub) ListSchedulableByPlatform(ctx context.Context, platform string) ([]Account, error) {
	s.platformCalls = append(s.platformCalls, platform)
	return append([]Account(nil), s.allAccounts...), nil
}

func TestAPIKeyService_GetCodexModelCatalog_UsesKeyGroupSchedulableAccounts(t *testing.T) {
	groupID := int64(42)
	apiKeyRepo := &apiKeyRepoStub{
		apiKey: &APIKey{
			ID:      7,
			UserID:  9,
			GroupID: &groupID,
			Group:   &Group{ID: groupID, Platform: PlatformOpenAI},
		},
	}
	accountRepo := &codexCatalogAccountRepoStub{
		groupAccounts: []Account{
			{
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"gpt-5.5":         "deepseek-v4-pro",
						"deepseek-v4-pro": "deepseek-v4-pro",
						"gpt-*":           "deepseek-v4-flash",
						"":                "ignored",
					},
				},
			},
			{
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"deepseek-v4-pro": "different-upstream",
						"minimax-m3":      "minimax-m3",
					},
				},
			},
		},
	}
	svc := &APIKeyService{
		apiKeyRepo:  apiKeyRepo,
		accountRepo: accountRepo,
	}

	catalog, err := svc.GetCodexModelCatalog(context.Background(), 7, 9)
	require.NoError(t, err)
	require.Equal(t, []codexCatalogGroupCall{{groupID: groupID, platform: PlatformOpenAI}}, accountRepo.groupCalls)
	require.Empty(t, accountRepo.platformCalls)

	slugs := make([]string, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		slugs = append(slugs, model.Slug)
		require.Equal(t, model.Slug != "", model.SupportedInAPI)
		require.Equal(t, "list", model.Visibility)
		require.Equal(t, 128000, model.ContextWindow)
		require.Equal(t, 128000, model.MaxContextWindow)
	}
	require.Contains(t, slugs, "deepseek-v4-pro")
	require.Contains(t, slugs, "gpt-5.5")
	require.Contains(t, slugs, "gpt-5.4")
	require.Contains(t, slugs, "minimax-m3")
	require.NotContains(t, slugs, "gpt-*")
	require.Len(t, slugs, len(uniqueStrings(slugs)))
}

func TestAPIKeyService_GetCodexModelCatalog_EnumeratesKnownModelsForOpenAccount(t *testing.T) {
	groupID := int64(42)
	apiKeyRepo := &apiKeyRepoStub{
		apiKey: &APIKey{
			ID:      7,
			UserID:  9,
			GroupID: &groupID,
			Group:   &Group{ID: groupID, Platform: PlatformOpenAI},
		},
	}
	accountRepo := &codexCatalogAccountRepoStub{
		groupAccounts: []Account{
			{
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Credentials: map[string]any{},
			},
		},
	}
	svc := &APIKeyService{
		apiKeyRepo:  apiKeyRepo,
		accountRepo: accountRepo,
	}

	catalog, err := svc.GetCodexModelCatalog(context.Background(), 7, 9)
	require.NoError(t, err)

	slugs := make([]string, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		slugs = append(slugs, model.Slug)
	}
	require.Contains(t, slugs, "gpt-5.5")
	require.Contains(t, slugs, "gpt-5.3-codex")
	require.Len(t, slugs, len(uniqueStrings(slugs)))
}

func TestAPIKeyService_GetCodexModelCatalog_RejectsNonOwner(t *testing.T) {
	apiKeyRepo := &apiKeyRepoStub{
		apiKey: &APIKey{
			ID:     7,
			UserID: 9,
		},
	}
	svc := &APIKeyService{apiKeyRepo: apiKeyRepo}

	_, err := svc.GetCodexModelCatalog(context.Background(), 7, 10)
	require.ErrorIs(t, err, ErrInsufficientPerms)
}

func uniqueStrings(values []string) map[string]struct{} {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	return seen
}
