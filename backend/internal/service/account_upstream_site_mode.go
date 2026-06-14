package service

import (
	"context"
	stderrors "errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const (
	CredentialUpstreamSiteMode             = "upstream_site_mode"
	CredentialUpstreamSiteBaseURL          = "upstream_site_base_url"
	CredentialUpstreamSiteType             = "upstream_site_type"
	CredentialUpstreamSiteUsername         = "upstream_site_username"
	CredentialUpstreamSitePassword         = "upstream_site_password"
	CredentialUpstreamSiteAccessToken      = "upstream_site_access_token"
	CredentialUpstreamSiteRefreshToken     = "upstream_site_refresh_token"
	CredentialUpstreamSiteTokenExpiresAt   = "upstream_site_token_expires_at"
	CredentialUpstreamSiteUserID           = "upstream_site_user_id"
	CredentialUpstreamSiteSessionCookie    = "upstream_site_session_cookie"
	CredentialUpstreamSiteSessionExpiresAt = "upstream_site_session_expires_at"

	ExtraUpstreamSiteMode             = "upstream_site_mode"
	ExtraUpstreamSiteType             = "upstream_site_type"
	ExtraUpstreamKeyRateMultiplier    = "upstream_key_rate_multiplier"
	ExtraUpstreamKeyGroupID           = "upstream_key_group_id"
	ExtraUpstreamKeyGroupName         = "upstream_key_group_name"
	ExtraUpstreamKeyID                = "upstream_key_id"
	ExtraUpstreamKeyName              = "upstream_key_name"
	ExtraUpstreamKeyMatchedField      = "upstream_key_matched_field"
	ExtraUpstreamKeyRateCheckedAt     = "upstream_key_rate_checked_at"
	ExtraUpstreamKeyRateResolveFailed = "upstream_key_rate_resolve_failed"
	ExtraUpstreamKeyRateResolveError  = "upstream_key_rate_resolve_error"
	ExtraUpstreamAccountBalance       = "upstream_account_balance"
)

const upstreamKeyRateCacheTTL = 2 * time.Hour
const upstreamKeyRateRefreshConcurrency = 4
const upstreamKeyRateResolveErrorMaxLen = 320

func (s *adminServiceImpl) prepareUpstreamSiteModeForCreate(ctx context.Context, input *CreateAccountInput) error {
	if input == nil || !shouldResolveUpstreamSiteMode(input.Platform, input.Type, input.Credentials) {
		return nil
	}
	if err := validateUpstreamSiteModeCredentials(input.Credentials); err != nil {
		return err
	}
	if input.Extra == nil {
		input.Extra = map[string]any{}
	}
	clearUpstreamSiteModeLoginState(input.Credentials)
	clearUpstreamSiteModeExtra(input.Extra)
	applyUpstreamSiteModeConfig(input.Extra, input.Credentials)
	return nil
}

func (s *adminServiceImpl) prepareUpstreamSiteModeForUpdate(ctx context.Context, account *Account, input *UpdateAccountInput) error {
	if account == nil || input == nil {
		return nil
	}
	credentials := account.Credentials
	if len(input.Credentials) > 0 {
		credentials = MergePreservingSensitiveCreds(account.Credentials, input.Credentials)
	}
	accountType := account.Type
	if strings.TrimSpace(input.Type) != "" {
		accountType = input.Type
	}
	wasUpstreamSiteMode := shouldResolveUpstreamSiteMode(account.Platform, accountType, account.Credentials)
	if !shouldResolveUpstreamSiteMode(account.Platform, accountType, credentials) {
		if wasUpstreamSiteMode && len(input.Credentials) > 0 {
			input.Credentials = credentials
			clearUpstreamSiteModeLoginState(input.Credentials)
			extra := cloneJSONMap(account.Extra)
			if input.Extra != nil {
				extra = input.Extra
			}
			if extra != nil {
				clearUpstreamSiteModeExtra(extra)
				input.Extra = extra
			}
		}
		return nil
	}
	if err := validateUpstreamSiteModeCredentials(credentials); err != nil {
		return err
	}
	if input.Credentials == nil {
		input.Credentials = map[string]any{}
	}
	input.Credentials = credentials
	if upstreamSiteLoginConfigChanged(account.Credentials, credentials) {
		clearUpstreamSiteModeLoginState(input.Credentials)
	}
	extra := cloneJSONMap(account.Extra)
	if input.Extra != nil {
		extra = input.Extra
	}
	if extra == nil {
		extra = map[string]any{}
	}
	clearUpstreamSiteModeExtra(extra)
	applyUpstreamSiteModeConfig(extra, credentials)
	input.Extra = extra
	return nil
}

func shouldResolveUpstreamSiteMode(platform, accountType string, credentials map[string]any) bool {
	if platform != PlatformOpenAI || accountType != AccountTypeAPIKey {
		return false
	}
	return boolCredential(credentials, CredentialUpstreamSiteMode)
}

func validateUpstreamSiteModeCredentials(credentials map[string]any) error {
	baseURL := credentialString(credentials, CredentialUpstreamSiteBaseURL)
	if baseURL == "" {
		baseURL = credentialString(credentials, "base_url")
	}
	apiKey := credentialString(credentials, "api_key")
	username := credentialString(credentials, CredentialUpstreamSiteUsername)
	password := credentialString(credentials, CredentialUpstreamSitePassword)
	if baseURL == "" {
		return infraerrors.BadRequest("UPSTREAM_SITE_BASE_URL_REQUIRED", "base_url is required for upstream site mode")
	}
	if apiKey == "" {
		return infraerrors.BadRequest("UPSTREAM_SITE_API_KEY_REQUIRED", "api_key is required for upstream site mode")
	}
	if username == "" {
		return infraerrors.BadRequest("UPSTREAM_SITE_USERNAME_REQUIRED", "upstream site username is required")
	}
	if password == "" {
		return infraerrors.BadRequest("UPSTREAM_SITE_PASSWORD_REQUIRED", "upstream site password is required")
	}
	return nil
}

func resolveUpstreamSiteModeRateFromCredentials(ctx context.Context, credentials map[string]any) (*ResolveUpstreamKeyRateResult, error) {
	baseURL := credentialString(credentials, CredentialUpstreamSiteBaseURL)
	if baseURL == "" {
		baseURL = credentialString(credentials, "base_url")
	}
	apiKey := credentialString(credentials, "api_key")
	username := credentialString(credentials, CredentialUpstreamSiteUsername)
	password := credentialString(credentials, CredentialUpstreamSitePassword)
	siteType := credentialString(credentials, CredentialUpstreamSiteType)
	if err := validateUpstreamSiteModeCredentials(credentials); err != nil {
		return nil, err
	}
	return resolveUpstreamKeyRate(ctx, ResolveUpstreamKeyRateInput{
		BaseURL:     baseURL,
		SiteType:    siteType,
		Username:    username,
		Email:       username,
		Password:    password,
		APIKey:      apiKey,
		Credentials: credentials,
	})
}

func applyUpstreamSiteModeConfig(extra map[string]any, credentials map[string]any) {
	if extra == nil {
		return
	}
	extra[ExtraUpstreamSiteMode] = true
	siteType := normalizeUpstreamSiteType(credentialString(credentials, CredentialUpstreamSiteType))
	if siteType == UpstreamSiteTypeSub2API || siteType == UpstreamSiteTypeNewAPI {
		extra[ExtraUpstreamSiteType] = siteType
	} else {
		extra[ExtraUpstreamSiteType] = UpstreamSiteTypeAuto
	}
}

func applyUpstreamSiteModeResult(extra map[string]any, result *ResolveUpstreamKeyRateResult) {
	if extra == nil || result == nil {
		return
	}
	extra[ExtraUpstreamSiteMode] = true
	extra[ExtraUpstreamSiteType] = result.SiteType
	extra[ExtraUpstreamKeyRateMultiplier] = result.RateMultiplier
	extra[ExtraUpstreamKeyRateCheckedAt] = time.Now().UTC().Format(time.RFC3339)
	extra[ExtraUpstreamKeyRateResolveFailed] = false
	delete(extra, ExtraUpstreamKeyRateResolveError)
	if result.GroupID != nil {
		extra[ExtraUpstreamKeyGroupID] = *result.GroupID
	} else {
		delete(extra, ExtraUpstreamKeyGroupID)
	}
	setOrDeleteStringExtra(extra, ExtraUpstreamKeyGroupName, result.GroupName)
	if result.KeyID != nil {
		extra[ExtraUpstreamKeyID] = *result.KeyID
	} else {
		delete(extra, ExtraUpstreamKeyID)
	}
	setOrDeleteStringExtra(extra, ExtraUpstreamKeyName, result.KeyName)
	setOrDeleteStringExtra(extra, ExtraUpstreamKeyMatchedField, result.MatchedField)
	if result.AccountBalance != nil {
		extra[ExtraUpstreamAccountBalance] = *result.AccountBalance
	} else {
		delete(extra, ExtraUpstreamAccountBalance)
	}
}

func buildUpstreamSiteModeResultUpdates(result *ResolveUpstreamKeyRateResult) map[string]any {
	updates := map[string]any{
		ExtraUpstreamSiteMode:             true,
		ExtraUpstreamSiteType:             result.SiteType,
		ExtraUpstreamKeyRateMultiplier:    result.RateMultiplier,
		ExtraUpstreamKeyRateCheckedAt:     time.Now().UTC().Format(time.RFC3339),
		ExtraUpstreamKeyRateResolveFailed: false,
		ExtraUpstreamKeyRateResolveError:  nil,
	}
	if result.GroupID != nil {
		updates[ExtraUpstreamKeyGroupID] = *result.GroupID
	} else {
		updates[ExtraUpstreamKeyGroupID] = nil
	}
	updates[ExtraUpstreamKeyGroupName] = nilIfEmpty(result.GroupName)
	if result.KeyID != nil {
		updates[ExtraUpstreamKeyID] = *result.KeyID
	} else {
		updates[ExtraUpstreamKeyID] = nil
	}
	updates[ExtraUpstreamKeyName] = nilIfEmpty(result.KeyName)
	updates[ExtraUpstreamKeyMatchedField] = nilIfEmpty(result.MatchedField)
	if result.AccountBalance != nil {
		updates[ExtraUpstreamAccountBalance] = *result.AccountBalance
	} else {
		updates[ExtraUpstreamAccountBalance] = nil
	}
	return updates
}

func nilIfEmpty(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func buildUpstreamSiteModeFailureUpdates(siteType string, err error) map[string]any {
	updates := map[string]any{
		ExtraUpstreamSiteMode:             true,
		ExtraUpstreamKeyRateCheckedAt:     time.Now().UTC().Format(time.RFC3339),
		ExtraUpstreamKeyRateResolveFailed: true,
		ExtraUpstreamKeyRateResolveError:  upstreamKeyRateErrorSummary(err),
	}
	siteType = normalizeUpstreamSiteType(siteType)
	if siteType == UpstreamSiteTypeSub2API || siteType == UpstreamSiteTypeNewAPI || siteType == UpstreamSiteTypeAuto {
		updates[ExtraUpstreamSiteType] = siteType
	}
	return updates
}

func upstreamKeyRateErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	appErr := mostSpecificApplicationError(err)
	parts := make([]string, 0, 2)
	if strings.TrimSpace(appErr.Reason) != "" {
		parts = append(parts, strings.TrimSpace(appErr.Reason))
	}
	if strings.TrimSpace(appErr.Message) != "" {
		parts = append(parts, strings.TrimSpace(appErr.Message))
	}
	if len(parts) == 0 {
		parts = append(parts, err.Error())
	}
	summary := logredact.RedactText(strings.Join(parts, ": "), "api_key", "key", "token", "authorization", "cookie", "set-cookie")
	if len(summary) > upstreamKeyRateResolveErrorMaxLen {
		summary = summary[:upstreamKeyRateResolveErrorMaxLen]
	}
	return summary
}

func mostSpecificApplicationError(err error) *infraerrors.ApplicationError {
	if err == nil {
		return nil
	}
	var appErr *infraerrors.ApplicationError
	for current := err; current != nil; current = stderrors.Unwrap(current) {
		var candidate *infraerrors.ApplicationError
		if stderrors.As(current, &candidate) && strings.TrimSpace(candidate.Reason) != "" {
			appErr = candidate
		}
		unwrapped := stderrors.Unwrap(current)
		if unwrapped == current {
			break
		}
	}
	if appErr != nil {
		return appErr
	}
	return infraerrors.FromError(err)
}

func upstreamKeyRateCacheNeedsRefresh(account *Account, now time.Time) bool {
	if account == nil || !shouldResolveUpstreamSiteMode(account.Platform, account.Type, account.Credentials) {
		return false
	}
	if account.Extra == nil {
		return true
	}
	checkedAt := parseExtraTime(account.Extra[ExtraUpstreamKeyRateCheckedAt])
	if checkedAt.IsZero() {
		return true
	}
	return !now.Before(checkedAt.Add(upstreamKeyRateCacheTTL))
}

func upstreamKeyRateMultiplierFromExtra(extra map[string]any, now time.Time) (float64, bool) {
	if extra == nil || !boolCredential(extra, ExtraUpstreamSiteMode) {
		return 0, false
	}
	checkedAt := parseExtraTime(extra[ExtraUpstreamKeyRateCheckedAt])
	if checkedAt.IsZero() || !now.Before(checkedAt.Add(upstreamKeyRateCacheTTL)) {
		return 0, false
	}
	parsed, ok := parseOptionalExtraFloat64(extra[ExtraUpstreamKeyRateMultiplier])
	if !ok || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

func refreshAccountUpstreamSiteModeRateIfStale(ctx context.Context, accountRepo AccountRepository, account *Account) *Account {
	if account == nil || accountRepo == nil || !upstreamKeyRateCacheNeedsRefresh(account, time.Now()) {
		return account
	}
	loginStateBefore := upstreamSiteLoginStateSnapshot(account.Credentials)
	result, err := resolveUpstreamSiteModeRateFromCredentials(ctx, account.Credentials)
	var updates map[string]any
	if err != nil {
		slog.Warn("upstream site rate refresh failed", "account_id", account.ID, "error", err)
		updates = buildUpstreamSiteModeFailureUpdates(credentialString(account.Credentials, CredentialUpstreamSiteType), err)
	} else {
		updates = buildUpstreamSiteModeResultUpdates(result)
	}
	if upstreamSiteLoginStateChanged(loginStateBefore, account.Credentials) {
		if credentialsErr := persistAccountCredentials(ctx, accountRepo, account, account.Credentials); credentialsErr != nil {
			slog.Warn("upstream site login state cache update failed", "account_id", account.ID, "error", credentialsErr)
		}
	}
	if updateErr := accountRepo.UpdateExtra(ctx, account.ID, updates); updateErr != nil {
		slog.Warn("upstream site rate cache update failed", "account_id", account.ID, "error", updateErr)
		return account
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	for key, value := range updates {
		account.Extra[key] = value
	}
	return account
}

func refreshAccountSliceUpstreamSiteModeRates(ctx context.Context, accountRepo AccountRepository, accounts []*Account) {
	if len(accounts) == 0 || accountRepo == nil {
		return
	}
	sem := make(chan struct{}, upstreamKeyRateRefreshConcurrency)
	var wg sync.WaitGroup
	for _, account := range accounts {
		if !upstreamKeyRateCacheNeedsRefresh(account, time.Now()) {
			continue
		}
		account := account
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			refreshAccountUpstreamSiteModeRateIfStale(ctx, accountRepo, account)
		}()
	}
	wg.Wait()
}

func (s *adminServiceImpl) refreshUpstreamSiteModeRatesForAccountValues(ctx context.Context, accounts []Account) {
	if len(accounts) == 0 || s == nil {
		return
	}
	pointers := make([]*Account, 0, len(accounts))
	for i := range accounts {
		pointers = append(pointers, &accounts[i])
	}
	refreshAccountSliceUpstreamSiteModeRates(ctx, s.accountRepo, pointers)
}

func clearUpstreamSiteModeExtra(extra map[string]any) {
	if extra == nil {
		return
	}
	delete(extra, ExtraUpstreamSiteMode)
	delete(extra, ExtraUpstreamSiteType)
	delete(extra, ExtraUpstreamKeyRateMultiplier)
	delete(extra, ExtraUpstreamKeyGroupID)
	delete(extra, ExtraUpstreamKeyGroupName)
	delete(extra, ExtraUpstreamKeyID)
	delete(extra, ExtraUpstreamKeyName)
	delete(extra, ExtraUpstreamKeyMatchedField)
	delete(extra, ExtraUpstreamKeyRateCheckedAt)
	delete(extra, ExtraUpstreamKeyRateResolveFailed)
	delete(extra, ExtraUpstreamKeyRateResolveError)
	delete(extra, ExtraUpstreamAccountBalance)
}

func clearUpstreamSiteModeLoginState(credentials map[string]any) {
	if credentials == nil {
		return
	}
	for _, key := range upstreamSiteLoginStateKeys {
		delete(credentials, key)
	}
}

var upstreamSiteLoginStateKeys = []string{
	CredentialUpstreamSiteAccessToken,
	CredentialUpstreamSiteRefreshToken,
	CredentialUpstreamSiteTokenExpiresAt,
	CredentialUpstreamSiteUserID,
	CredentialUpstreamSiteSessionCookie,
	CredentialUpstreamSiteSessionExpiresAt,
}

type upstreamSiteLoginState struct {
	values map[string]any
}

func upstreamSiteLoginStateSnapshot(credentials map[string]any) upstreamSiteLoginState {
	values := make(map[string]any, len(upstreamSiteLoginStateKeys))
	for _, key := range upstreamSiteLoginStateKeys {
		if credentials != nil {
			values[key] = credentials[key]
		}
	}
	return upstreamSiteLoginState{values: values}
}

func upstreamSiteLoginStateChanged(before upstreamSiteLoginState, credentials map[string]any) bool {
	for _, key := range upstreamSiteLoginStateKeys {
		if !jsonLikeEqual(before.values[key], credentials[key]) {
			return true
		}
	}
	return false
}

func upstreamSiteLoginConfigChanged(existing, next map[string]any) bool {
	for _, key := range []string{
		CredentialUpstreamSiteBaseURL,
		CredentialUpstreamSiteType,
		CredentialUpstreamSiteUsername,
		CredentialUpstreamSitePassword,
		"base_url",
	} {
		if credentialString(existing, key) != credentialString(next, key) {
			return true
		}
	}
	return false
}

func jsonLikeEqual(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

func setOrDeleteStringExtra(extra map[string]any, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		delete(extra, key)
		return
	}
	extra[key] = value
}

func credentialString(credentials map[string]any, key string) string {
	if credentials == nil {
		return ""
	}
	value, _ := credentials[key].(string)
	return strings.TrimSpace(value)
}

func boolCredential(credentials map[string]any, key string) bool {
	if credentials == nil {
		return false
	}
	switch value := credentials[key].(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}
