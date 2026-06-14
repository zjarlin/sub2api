package service

import (
	"context"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestPrepareUpstreamSiteModeForUpdateDisablesAndClearsExtra(t *testing.T) {
	svc := &adminServiceImpl{}
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url":                     "https://upstream.example/v1",
			"api_key":                      "sk-existing",
			CredentialUpstreamSiteMode:     true,
			CredentialUpstreamSiteBaseURL:  "https://upstream.example",
			CredentialUpstreamSiteUsername: "owner",
			CredentialUpstreamSitePassword: "secret",
		},
		Extra: map[string]any{
			ExtraUpstreamSiteMode:            true,
			ExtraUpstreamSiteType:            UpstreamSiteTypeSub2API,
			ExtraUpstreamKeyRateMultiplier:   0.5,
			ExtraUpstreamKeyGroupName:        "default",
			ExtraUpstreamKeyRateResolveError: "old error",
			"quota_used":                     12,
		},
	}
	input := &UpdateAccountInput{
		Credentials: map[string]any{
			"base_url":                     "https://upstream.example/v1",
			CredentialUpstreamSiteMode:     false,
			CredentialUpstreamSitePassword: "",
		},
	}

	err := svc.prepareUpstreamSiteModeForUpdate(context.Background(), account, input)
	require.NoError(t, err)
	require.Equal(t, "sk-existing", input.Credentials["api_key"])
	require.Equal(t, false, input.Credentials[CredentialUpstreamSiteMode])
	require.Equal(t, "", input.Credentials[CredentialUpstreamSitePassword])
	require.NotContains(t, input.Credentials, CredentialUpstreamSiteBaseURL)
	require.NotContains(t, input.Credentials, CredentialUpstreamSiteUsername)
	require.NotContains(t, input.Extra, ExtraUpstreamSiteMode)
	require.NotContains(t, input.Extra, ExtraUpstreamSiteType)
	require.NotContains(t, input.Extra, ExtraUpstreamKeyRateMultiplier)
	require.NotContains(t, input.Extra, ExtraUpstreamKeyGroupName)
	require.NotContains(t, input.Extra, ExtraUpstreamKeyRateResolveError)
	require.Equal(t, float64(12), input.Extra["quota_used"])
}

func TestBuildUpstreamSiteModeFailureUpdatesStoresRedactedReason(t *testing.T) {
	err := infraerrors.New(502, "UPSTREAM_RATE_LOGIN_FAILED", "password=secret api_key=sk-secret token=abc failed")

	updates := buildUpstreamSiteModeFailureUpdates(UpstreamSiteTypeNewAPI, err)

	require.Equal(t, true, updates[ExtraUpstreamSiteMode])
	require.Equal(t, true, updates[ExtraUpstreamKeyRateResolveFailed])
	require.Equal(t, UpstreamSiteTypeNewAPI, updates[ExtraUpstreamSiteType])
	reason, ok := updates[ExtraUpstreamKeyRateResolveError].(string)
	require.True(t, ok)
	require.Contains(t, reason, "UPSTREAM_RATE_LOGIN_FAILED")
	require.NotContains(t, reason, "secret")
	require.NotContains(t, reason, "sk-secret")
	require.NotContains(t, reason, "abc")
}

func TestBuildUpstreamSiteModeResultUpdatesClearsFailureReason(t *testing.T) {
	result := &ResolveUpstreamKeyRateResult{
		SiteType:       UpstreamSiteTypeSub2API,
		RateMultiplier: 0.5,
	}

	updates := buildUpstreamSiteModeResultUpdates(result)

	require.Equal(t, false, updates[ExtraUpstreamKeyRateResolveFailed])
	require.Contains(t, updates, ExtraUpstreamKeyRateResolveError)
	require.Nil(t, updates[ExtraUpstreamKeyRateResolveError])
}

func TestUpstreamKeyRateMultiplierUsesFreshCachedValueAfterRefreshFailure(t *testing.T) {
	extra := map[string]any{
		ExtraUpstreamSiteMode:             true,
		ExtraUpstreamKeyRateMultiplier:    0.05,
		ExtraUpstreamKeyRateCheckedAt:     time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339),
		ExtraUpstreamKeyRateResolveFailed: true,
		ExtraUpstreamKeyRateResolveError:  "temporary upstream failure",
	}

	rate, ok := upstreamKeyRateMultiplierFromExtra(extra, time.Now().UTC())

	require.True(t, ok)
	require.Equal(t, 0.05, rate)
}

func TestUpstreamKeyRateErrorSummaryPrefersInnerApplicationError(t *testing.T) {
	inner := infraerrors.NotFound("UPSTREAM_RATE_KEY_NOT_FOUND", "target key was not found")
	outer := infraerrors.New(502, "UPSTREAM_RATE_AUTO_DETECT_FAILED", "auto detect failed").WithCause(inner)

	reason := upstreamKeyRateErrorSummary(outer)

	require.Contains(t, reason, "UPSTREAM_RATE_KEY_NOT_FOUND")
	require.Contains(t, reason, "target key was not found")
	require.NotContains(t, reason, "UPSTREAM_RATE_AUTO_DETECT_FAILED")
}
