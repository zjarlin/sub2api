package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const (
	upstreamKeyRateDefaultLoginPath   = "/api/v1/auth/login"
	upstreamKeyRateDefaultRefreshPath = "/api/v1/auth/refresh"
	upstreamKeyRateDefaultKeysPath    = "/api/v1/keys"
	upstreamKeyRateDefaultGroupsPath  = "/api/v1/groups/available"
	upstreamKeyRateDefaultProfilePath = "/api/v1/user/profile"
	upstreamKeyRateNewAPILoginPath    = "/api/user/login"
	upstreamKeyRateNewAPITokensPath   = "/api/token/"
	upstreamKeyRateNewAPIGroupsPath   = "/api/user/groups"
	upstreamKeyRateNewAPISelfPath     = "/api/user/self"
	upstreamKeyRateDefaultPageSize    = 100
	upstreamKeyRateDefaultMaxPages    = 5
	upstreamKeyRateMaxPageSize        = 500
	upstreamKeyRateMaxPages           = 20
	upstreamKeyRateRequestTimeout     = 20 * time.Second
	upstreamKeyRateMaxBodyBytes       = int64(2 * 1024 * 1024)
	upstreamKeyRateNewAPIQuotaPerUSD  = 500000.0
	upstreamSiteLoginRefreshSkew      = 2 * time.Minute
	upstreamSiteNewAPISessionTTL      = 30 * 24 * time.Hour
)

const (
	UpstreamSiteTypeAuto    = "auto"
	UpstreamSiteTypeSub2API = "sub2api"
	UpstreamSiteTypeNewAPI  = "new-api"
)

type ResolveUpstreamKeyRateInput struct {
	BaseURL   string `json:"base_url"`
	LoginPath string `json:"login_path"`
	KeysPath  string `json:"keys_path"`
	SiteType  string `json:"site_type"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	APIKey    string `json:"api_key"`
	PageSize  int    `json:"page_size"`
	MaxPages  int    `json:"max_pages"`

	Credentials map[string]any `json:"-"`
}

type ResolveUpstreamKeyRateResult struct {
	RateMultiplier float64  `json:"rate_multiplier"`
	SiteType       string   `json:"site_type,omitempty"`
	AccountBalance *float64 `json:"account_balance,omitempty"`
	GroupID        *int64   `json:"group_id,omitempty"`
	GroupName      string   `json:"group_name,omitempty"`
	KeyID          *int64   `json:"key_id,omitempty"`
	KeyName        string   `json:"key_name,omitempty"`
	MatchedField   string   `json:"matched_field,omitempty"`
}

type TestUpstreamConsoleLoginInput struct {
	BaseURL   string `json:"base_url"`
	LoginPath string `json:"login_path"`
	SiteType  string `json:"site_type"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	Password  string `json:"password"`
}

type TestUpstreamConsoleLoginResult struct {
	SiteType         string   `json:"site_type"`
	UserID           string   `json:"user_id,omitempty"`
	HasAccessToken   bool     `json:"has_access_token"`
	HasSessionCookie bool     `json:"has_session_cookie"`
	AccountBalance   *float64 `json:"account_balance,omitempty"`
}

type SwitchUpstreamKeyGroupInput struct {
	GroupName string `json:"group_name"`
}

type UpstreamKeyGroupOption struct {
	ID             *int64  `json:"id,omitempty"`
	Name           string  `json:"name"`
	RateMultiplier float64 `json:"rate_multiplier"`
	Description    string  `json:"description,omitempty"`
}

type newAPIUpstreamMatchedToken struct {
	Item   map[string]any
	KeyID  int64
	Result *ResolveUpstreamKeyRateResult
}

func (s *adminServiceImpl) ResolveUpstreamKeyRate(ctx context.Context, input ResolveUpstreamKeyRateInput) (*ResolveUpstreamKeyRateResult, error) {
	return resolveUpstreamKeyRate(ctx, input)
}

func (s *adminServiceImpl) TestUpstreamConsoleLogin(ctx context.Context, input TestUpstreamConsoleLoginInput) (*TestUpstreamConsoleLoginResult, error) {
	return testUpstreamConsoleLogin(ctx, input)
}

func (s *adminServiceImpl) ListUpstreamKeyGroups(ctx context.Context, accountID int64) ([]UpstreamKeyGroupOption, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
		return nil, infraerrors.BadRequest("UPSTREAM_GROUP_LIST_ACCOUNT_UNSUPPORTED", "upstream groups only support OpenAI API key accounts")
	}
	if !shouldResolveUpstreamSiteMode(account.Platform, account.Type, account.Credentials) {
		return nil, infraerrors.BadRequest("UPSTREAM_GROUP_LIST_SITE_MODE_REQUIRED", "upstream site mode must be enabled before listing upstream groups")
	}
	if err := validateUpstreamSiteModeCredentials(account.Credentials); err != nil {
		return nil, err
	}

	baseURL := credentialString(account.Credentials, CredentialUpstreamSiteBaseURL)
	if baseURL == "" {
		baseURL = credentialString(account.Credentials, "base_url")
	}
	loginStateBefore := upstreamSiteLoginStateSnapshot(account.Credentials)
	resolveInput := ResolveUpstreamKeyRateInput{
		BaseURL:     baseURL,
		SiteType:    resolveStoredUpstreamSiteType(account.Credentials, account.Extra),
		Username:    credentialString(account.Credentials, CredentialUpstreamSiteUsername),
		Email:       credentialString(account.Credentials, CredentialUpstreamSiteUsername),
		Password:    credentialString(account.Credentials, CredentialUpstreamSitePassword),
		APIKey:      credentialString(account.Credentials, "api_key"),
		Credentials: account.Credentials,
	}
	groups, err := listUpstreamKeyGroups(ctx, resolveInput)
	if err != nil {
		return nil, err
	}
	if upstreamSiteLoginStateChanged(loginStateBefore, account.Credentials) {
		if err := persistAccountCredentials(ctx, s.accountRepo, account, account.Credentials); err != nil {
			return nil, err
		}
	}
	return groups, nil
}

func (s *adminServiceImpl) SwitchUpstreamKeyGroup(ctx context.Context, accountID int64, input SwitchUpstreamKeyGroupInput) (*Account, error) {
	targetGroup := strings.TrimSpace(input.GroupName)
	if targetGroup == "" {
		return nil, infraerrors.BadRequest("UPSTREAM_GROUP_NAME_REQUIRED", "group_name is required")
	}

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
		return nil, infraerrors.BadRequest("UPSTREAM_GROUP_SWITCH_ACCOUNT_UNSUPPORTED", "upstream group switching only supports OpenAI API key accounts")
	}
	if !shouldResolveUpstreamSiteMode(account.Platform, account.Type, account.Credentials) {
		return nil, infraerrors.BadRequest("UPSTREAM_GROUP_SWITCH_SITE_MODE_REQUIRED", "upstream site mode must be enabled before switching the upstream group")
	}

	baseURL := credentialString(account.Credentials, CredentialUpstreamSiteBaseURL)
	if baseURL == "" {
		baseURL = credentialString(account.Credentials, "base_url")
	}
	apiKey := credentialString(account.Credentials, "api_key")
	username := credentialString(account.Credentials, CredentialUpstreamSiteUsername)
	password := credentialString(account.Credentials, CredentialUpstreamSitePassword)
	if err := validateUpstreamSiteModeCredentials(account.Credentials); err != nil {
		return nil, err
	}

	loginStateBefore := upstreamSiteLoginStateSnapshot(account.Credentials)
	result, err := switchUpstreamKeyGroup(ctx, ResolveUpstreamKeyRateInput{
		BaseURL:     baseURL,
		SiteType:    resolveStoredUpstreamSiteType(account.Credentials, account.Extra),
		Username:    username,
		Email:       username,
		Password:    password,
		APIKey:      apiKey,
		Credentials: account.Credentials,
	}, targetGroup)
	if err != nil {
		return nil, err
	}
	if upstreamSiteLoginStateChanged(loginStateBefore, account.Credentials) {
		if err := persistAccountCredentials(ctx, s.accountRepo, account, account.Credentials); err != nil {
			return nil, err
		}
	}
	if err := s.accountRepo.UpdateExtra(ctx, accountID, buildUpstreamSiteModeResultUpdates(result)); err != nil {
		return nil, err
	}
	return s.accountRepo.GetByID(ctx, accountID)
}

func resolveStoredUpstreamSiteType(credentials map[string]any, extra map[string]any) string {
	siteType := normalizeUpstreamSiteType(credentialString(credentials, CredentialUpstreamSiteType))
	if siteType == UpstreamSiteTypeSub2API || siteType == UpstreamSiteTypeNewAPI {
		return siteType
	}
	if extraType := normalizeUpstreamSiteType(stringField(extra, ExtraUpstreamSiteType)); extraType == UpstreamSiteTypeSub2API {
		return UpstreamSiteTypeSub2API
	}
	return UpstreamSiteTypeAuto
}

func resolveUpstreamKeyRate(ctx context.Context, input ResolveUpstreamKeyRateInput) (*ResolveUpstreamKeyRateResult, error) {
	normalized, err := normalizeResolveUpstreamKeyRateInput(input)
	if err != nil {
		return nil, err
	}

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:           upstreamKeyRateRequestTimeout,
		AllowPrivateHosts: true,
	})
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_HTTP_CLIENT_FAILED", "failed to create upstream HTTP client").WithCause(err)
	}

	siteTypes := resolveUpstreamSiteTypeCandidates(normalized.SiteType)
	var lastErr error
	for _, siteType := range siteTypes {
		result, err := resolveUpstreamKeyRateBySiteType(ctx, client, normalized, siteType)
		if err == nil {
			result.SiteType = siteType
			return result, nil
		}
		lastErr = err
	}
	if lastErr != nil && normalized.SiteType != UpstreamSiteTypeAuto {
		return nil, lastErr
	}
	if lastErr != nil {
		return nil, infraerrors.New(
			http.StatusBadGateway,
			"UPSTREAM_RATE_AUTO_DETECT_FAILED",
			"no supported upstream console login succeeded or the key could not be resolved",
		).WithCause(lastErr)
	}
	return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_AUTO_DETECT_FAILED", "no supported upstream console login succeeded")
}

func normalizeResolveUpstreamKeyRateInput(input ResolveUpstreamKeyRateInput) (ResolveUpstreamKeyRateInput, error) {
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	input.LoginPath = strings.TrimSpace(input.LoginPath)
	input.KeysPath = strings.TrimSpace(input.KeysPath)
	input.SiteType = normalizeUpstreamSiteType(input.SiteType)
	input.Username = strings.TrimSpace(input.Username)
	input.Email = strings.TrimSpace(input.Email)
	input.Password = strings.TrimSpace(input.Password)
	input.APIKey = normalizeComparableAPIKey(input.APIKey)
	if input.Email == "" {
		input.Email = input.Username
	}
	if input.Username == "" {
		input.Username = input.Email
	}

	if input.BaseURL == "" {
		return input, infraerrors.BadRequest("UPSTREAM_RATE_BASE_URL_REQUIRED", "base_url is required")
	}
	base, err := parseUpstreamBaseURL(input.BaseURL)
	if err != nil {
		return input, err
	}
	input.BaseURL = base.String()

	if input.LoginPath == "" {
		input.LoginPath = upstreamKeyRateDefaultLoginPath
	}
	if input.KeysPath == "" {
		input.KeysPath = upstreamKeyRateDefaultKeysPath
	}
	if input.Email == "" {
		return input, infraerrors.BadRequest("UPSTREAM_RATE_USERNAME_REQUIRED", "username or email is required")
	}
	if input.Password == "" {
		return input, infraerrors.BadRequest("UPSTREAM_RATE_PASSWORD_REQUIRED", "password is required")
	}
	if input.APIKey == "" {
		return input, infraerrors.BadRequest("UPSTREAM_RATE_API_KEY_REQUIRED", "api_key is required")
	}
	if input.PageSize <= 0 {
		input.PageSize = upstreamKeyRateDefaultPageSize
	}
	if input.PageSize > upstreamKeyRateMaxPageSize {
		input.PageSize = upstreamKeyRateMaxPageSize
	}
	if input.MaxPages <= 0 {
		input.MaxPages = upstreamKeyRateDefaultMaxPages
	}
	if input.MaxPages > upstreamKeyRateMaxPages {
		input.MaxPages = upstreamKeyRateMaxPages
	}
	return input, nil
}

func normalizeTestUpstreamConsoleLoginInput(input TestUpstreamConsoleLoginInput) (ResolveUpstreamKeyRateInput, error) {
	normalized := ResolveUpstreamKeyRateInput{
		BaseURL:   strings.TrimSpace(input.BaseURL),
		LoginPath: strings.TrimSpace(input.LoginPath),
		SiteType:  normalizeUpstreamSiteType(input.SiteType),
		Username:  strings.TrimSpace(input.Username),
		Email:     strings.TrimSpace(input.Email),
		Password:  strings.TrimSpace(input.Password),
	}
	if normalized.Email == "" {
		normalized.Email = normalized.Username
	}
	if normalized.Username == "" {
		normalized.Username = normalized.Email
	}
	if normalized.BaseURL == "" {
		return normalized, infraerrors.BadRequest("UPSTREAM_RATE_BASE_URL_REQUIRED", "base_url is required")
	}
	base, err := parseUpstreamBaseURL(normalized.BaseURL)
	if err != nil {
		return normalized, err
	}
	normalized.BaseURL = base.String()
	if normalized.LoginPath == "" {
		normalized.LoginPath = upstreamKeyRateDefaultLoginPath
	}
	if normalized.Email == "" {
		return normalized, infraerrors.BadRequest("UPSTREAM_RATE_USERNAME_REQUIRED", "username or email is required")
	}
	if normalized.Password == "" {
		return normalized, infraerrors.BadRequest("UPSTREAM_RATE_PASSWORD_REQUIRED", "password is required")
	}
	return normalized, nil
}

func testUpstreamConsoleLogin(ctx context.Context, input TestUpstreamConsoleLoginInput) (*TestUpstreamConsoleLoginResult, error) {
	normalized, err := normalizeTestUpstreamConsoleLoginInput(input)
	if err != nil {
		return nil, err
	}

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:           upstreamKeyRateRequestTimeout,
		AllowPrivateHosts: true,
	})
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_HTTP_CLIENT_FAILED", "failed to create upstream HTTP client").WithCause(err)
	}

	var lastErr error
	for _, siteType := range resolveUpstreamSiteTypeCandidates(normalized.SiteType) {
		result, err := testUpstreamConsoleLoginBySiteType(ctx, client, normalized, siteType)
		if err == nil {
			result.SiteType = siteType
			return result, nil
		}
		lastErr = err
	}
	if lastErr != nil && normalized.SiteType != UpstreamSiteTypeAuto {
		return nil, lastErr
	}
	if lastErr != nil {
		return nil, infraerrors.New(
			http.StatusBadGateway,
			"UPSTREAM_LOGIN_AUTO_DETECT_FAILED",
			"no supported upstream console login succeeded",
		).WithCause(lastErr)
	}
	return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_LOGIN_AUTO_DETECT_FAILED", "no supported upstream console login succeeded")
}

func testUpstreamConsoleLoginBySiteType(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput, siteType string) (*TestUpstreamConsoleLoginResult, error) {
	switch siteType {
	case UpstreamSiteTypeSub2API:
		login, err := loginUpstreamForAccess(ctx, client, input)
		if err != nil {
			return nil, err
		}
		if login.Balance == nil {
			if balance, balanceErr := fetchUpstreamAccountBalance(ctx, client, input.BaseURL, login.Token); balanceErr == nil {
				login.Balance = balance
			}
		}
		return &TestUpstreamConsoleLoginResult{
			SiteType:       siteType,
			HasAccessToken: strings.TrimSpace(login.Token) != "",
			AccountBalance: login.Balance,
		}, nil
	case UpstreamSiteTypeNewAPI:
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, infraerrors.InternalServer("UPSTREAM_RATE_COOKIE_JAR_FAILED", "failed to create upstream cookie jar").WithCause(err)
		}
		cookieClient := *client
		cookieClient.Jar = jar
		login, err := loginNewAPIUpstream(ctx, &cookieClient, input)
		if err != nil {
			return nil, err
		}
		base, parseErr := parseUpstreamBaseURL(input.BaseURL)
		hasCookie := parseErr == nil && len(jar.Cookies(base)) > 0
		hasToken := strings.TrimSpace(login.Token) != ""
		if !hasCookie && !hasToken {
			return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_LOGIN_STATE_MISSING", "upstream new-api login did not return a reusable session cookie or access token")
		}
		return &TestUpstreamConsoleLoginResult{
			SiteType:         siteType,
			UserID:           login.UserID,
			HasAccessToken:   hasToken,
			HasSessionCookie: hasCookie,
			AccountBalance:   login.Balance,
		}, nil
	default:
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_SITE_TYPE_INVALID", "unsupported upstream site_type")
	}
}

func resolveUpstreamKeyRateBySiteType(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput, siteType string) (*ResolveUpstreamKeyRateResult, error) {
	switch siteType {
	case UpstreamSiteTypeSub2API:
		login, err := loginUpstreamForAccess(ctx, client, input)
		if err != nil {
			return nil, err
		}
		if login.Balance == nil {
			if balance, balanceErr := fetchUpstreamAccountBalance(ctx, client, input.BaseURL, login.Token); balanceErr == nil {
				login.Balance = balance
			}
		}
		result, err := findUpstreamKeyRate(ctx, client, input, login.Token)
		if err != nil {
			if login.ReusedStoredState && isUpstreamLoginStateAuthFailure(err) {
				clearUpstreamSiteModeLoginState(input.Credentials)
				login, err = loginUpstreamForAccess(ctx, client, input)
				if err != nil {
					return nil, err
				}
				if login.Balance == nil {
					if balance, balanceErr := fetchUpstreamAccountBalance(ctx, client, input.BaseURL, login.Token); balanceErr == nil {
						login.Balance = balance
					}
				}
				result, err = findUpstreamKeyRate(ctx, client, input, login.Token)
				if err == nil {
					if login.Balance != nil {
						result.AccountBalance = login.Balance
					}
					return result, nil
				}
			}
			return nil, err
		}
		if login.Balance != nil {
			result.AccountBalance = login.Balance
		}
		return result, nil
	case UpstreamSiteTypeNewAPI:
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, infraerrors.InternalServer("UPSTREAM_RATE_COOKIE_JAR_FAILED", "failed to create upstream cookie jar").WithCause(err)
		}
		cookieClient := *client
		cookieClient.Jar = jar
		login, err := loginNewAPIUpstream(ctx, &cookieClient, input)
		if err != nil {
			return nil, err
		}
		if login.Balance == nil {
			if balance, balanceErr := fetchNewAPIAccountBalance(ctx, &cookieClient, input.BaseURL, login.Token, login.UserID); balanceErr == nil {
				login.Balance = balance
			}
		}
		result, err := findNewAPIUpstreamKeyRate(ctx, &cookieClient, input, login.Token, login.UserID)
		if err != nil {
			if login.ReusedStoredState && isUpstreamLoginStateAuthFailure(err) {
				clearUpstreamSiteModeLoginState(input.Credentials)
				login, err = loginNewAPIUpstream(ctx, &cookieClient, input)
				if err != nil {
					return nil, err
				}
				if login.Balance == nil {
					if balance, balanceErr := fetchNewAPIAccountBalance(ctx, &cookieClient, input.BaseURL, login.Token, login.UserID); balanceErr == nil {
						login.Balance = balance
					}
				}
				result, err = findNewAPIUpstreamKeyRate(ctx, &cookieClient, input, login.Token, login.UserID)
				if err == nil {
					if login.Balance != nil {
						result.AccountBalance = login.Balance
					}
					return result, nil
				}
			}
			return nil, err
		}
		if login.Balance != nil {
			result.AccountBalance = login.Balance
		}
		return result, nil
	default:
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_SITE_TYPE_INVALID", "unsupported upstream site_type")
	}
}

func resolveUpstreamSiteTypeCandidates(siteType string) []string {
	switch normalizeUpstreamSiteType(siteType) {
	case UpstreamSiteTypeAuto:
		return []string{UpstreamSiteTypeSub2API, UpstreamSiteTypeNewAPI}
	case UpstreamSiteTypeSub2API:
		return []string{UpstreamSiteTypeSub2API}
	case UpstreamSiteTypeNewAPI:
		return []string{UpstreamSiteTypeNewAPI}
	default:
		return []string{siteType}
	}
}

func normalizeUpstreamSiteType(siteType string) string {
	switch strings.ToLower(strings.TrimSpace(siteType)) {
	case "", UpstreamSiteTypeAuto:
		return UpstreamSiteTypeAuto
	case UpstreamSiteTypeSub2API:
		return UpstreamSiteTypeSub2API
	case UpstreamSiteTypeNewAPI, "newapi":
		return UpstreamSiteTypeNewAPI
	default:
		return strings.ToLower(strings.TrimSpace(siteType))
	}
}

func parseUpstreamBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_BASE_URL_INVALID", "base_url is invalid").WithCause(err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_BASE_URL_INVALID", "base_url must use http or https")
	}
	if parsed.Host == "" {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_BASE_URL_INVALID", "base_url host is required")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/api/v1")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/v1")
	return parsed, nil
}

type upstreamLoginResult struct {
	Token             string
	RefreshToken      string
	ExpiresIn         int
	Balance           *float64
	UserID            string
	ReusedStoredState bool
}

func loginUpstreamForAccessToken(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput) (string, error) {
	login, err := loginUpstreamForAccess(ctx, client, input)
	if err != nil {
		return "", err
	}
	return login.Token, nil
}

func loginUpstreamForAccess(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput) (*upstreamLoginResult, error) {
	if login := cachedSub2APIUpstreamLogin(input.Credentials); login != nil {
		return login, nil
	}
	if refreshToken := credentialString(input.Credentials, CredentialUpstreamSiteRefreshToken); refreshToken != "" {
		if login, err := refreshSub2APIUpstreamLogin(ctx, client, input, refreshToken); err == nil {
			rememberSub2APIUpstreamLogin(input.Credentials, login, refreshToken)
			return login, nil
		}
	}

	loginURL, err := buildUpstreamConsoleURL(input.BaseURL, input.LoginPath, 0, 0)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{
		"email":    input.Email,
		"password": input.Password,
	})
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_LOGIN_PAYLOAD_FAILED", "failed to encode upstream login payload").WithCause(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(body))
	if err != nil {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_LOGIN_URL_INVALID", "login URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	payload, statusCode, err := doUpstreamJSON(client, req)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_LOGIN_REQUEST_FAILED", "upstream login request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_LOGIN_FAILED", fmt.Sprintf("upstream login failed with HTTP %d", statusCode))
	}
	if err := assertUpstreamEnvelopeSuccess(payload, "UPSTREAM_RATE_LOGIN_FAILED", "upstream login failed"); err != nil {
		return nil, err
	}

	token := extractUpstreamAccessToken(payload)
	if token == "" {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_TOKEN_MISSING", "upstream login response did not contain access_token")
	}
	login := &upstreamLoginResult{
		Token:        token,
		RefreshToken: extractUpstreamRefreshToken(payload),
		ExpiresIn:    extractUpstreamExpiresIn(payload),
		Balance:      extractUpstreamAccountBalance(payload),
		UserID:       extractUpstreamUserID(payload),
	}
	rememberSub2APIUpstreamLogin(input.Credentials, login, "")
	return login, nil
}

func refreshSub2APIUpstreamLogin(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput, refreshToken string) (*upstreamLoginResult, error) {
	refreshURL, err := buildUpstreamConsoleURL(input.BaseURL, upstreamKeyRateDefaultRefreshPath, 0, 0)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_REFRESH_PAYLOAD_FAILED", "failed to encode upstream refresh payload").WithCause(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, bytes.NewReader(body))
	if err != nil {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_REFRESH_URL_INVALID", "refresh URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	payload, statusCode, err := doUpstreamJSON(client, req)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_REFRESH_REQUEST_FAILED", "upstream token refresh request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_REFRESH_FAILED", fmt.Sprintf("upstream token refresh failed with HTTP %d", statusCode))
	}
	if err := assertUpstreamEnvelopeSuccess(payload, "UPSTREAM_RATE_REFRESH_FAILED", "upstream token refresh failed"); err != nil {
		return nil, err
	}
	token := extractUpstreamAccessToken(payload)
	if token == "" {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_TOKEN_MISSING", "upstream refresh response did not contain access_token")
	}
	return &upstreamLoginResult{
		Token:        token,
		RefreshToken: extractUpstreamRefreshToken(payload),
		ExpiresIn:    extractUpstreamExpiresIn(payload),
		Balance:      extractUpstreamAccountBalance(payload),
		UserID:       extractUpstreamUserID(payload),
	}, nil
}

func cachedSub2APIUpstreamLogin(credentials map[string]any) *upstreamLoginResult {
	token := credentialString(credentials, CredentialUpstreamSiteAccessToken)
	if token == "" {
		return nil
	}
	expiresAt := parseExtraTime(credentials[CredentialUpstreamSiteTokenExpiresAt])
	if expiresAt.IsZero() || !time.Now().UTC().Add(upstreamSiteLoginRefreshSkew).Before(expiresAt) {
		return nil
	}
	return &upstreamLoginResult{
		Token:             token,
		UserID:            credentialString(credentials, CredentialUpstreamSiteUserID),
		ReusedStoredState: true,
	}
}

func rememberSub2APIUpstreamLogin(credentials map[string]any, login *upstreamLoginResult, fallbackRefreshToken string) {
	if credentials == nil || login == nil || strings.TrimSpace(login.Token) == "" {
		return
	}
	credentials[CredentialUpstreamSiteAccessToken] = strings.TrimSpace(login.Token)
	refreshToken := strings.TrimSpace(login.RefreshToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(fallbackRefreshToken)
	}
	if refreshToken != "" {
		credentials[CredentialUpstreamSiteRefreshToken] = refreshToken
	}
	if login.ExpiresIn > 0 {
		credentials[CredentialUpstreamSiteTokenExpiresAt] = time.Now().UTC().Add(time.Duration(login.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if strings.TrimSpace(login.UserID) != "" {
		credentials[CredentialUpstreamSiteUserID] = strings.TrimSpace(login.UserID)
	}
}

func cachedNewAPIUpstreamLogin(client *http.Client, input ResolveUpstreamKeyRateInput) *upstreamLoginResult {
	credentials := input.Credentials
	userID := credentialString(credentials, CredentialUpstreamSiteUserID)
	token := credentialString(credentials, CredentialUpstreamSiteAccessToken)
	if token != "" && userID != "" {
		return &upstreamLoginResult{
			Token:             token,
			UserID:            userID,
			ReusedStoredState: true,
		}
	}

	sessionCookie := credentialString(credentials, CredentialUpstreamSiteSessionCookie)
	sessionExpiresAt := parseExtraTime(credentials[CredentialUpstreamSiteSessionExpiresAt])
	if sessionCookie == "" || userID == "" || sessionExpiresAt.IsZero() || !time.Now().UTC().Add(upstreamSiteLoginRefreshSkew).Before(sessionExpiresAt) {
		return nil
	}
	if applyStoredSessionCookie(client, input.BaseURL, sessionCookie) {
		return &upstreamLoginResult{
			UserID:            userID,
			ReusedStoredState: true,
		}
	}
	return nil
}

func rememberNewAPIUpstreamLogin(credentials map[string]any, baseURL string, login *upstreamLoginResult, headers http.Header) {
	if credentials == nil || login == nil {
		return
	}
	if token := strings.TrimSpace(login.Token); token != "" {
		credentials[CredentialUpstreamSiteAccessToken] = token
	}
	if userID := strings.TrimSpace(login.UserID); userID != "" {
		credentials[CredentialUpstreamSiteUserID] = userID
	}
	cookieValue, expiresAt := extractSessionCookie(headers)
	if cookieValue == "" {
		return
	}
	credentials[CredentialUpstreamSiteSessionCookie] = cookieValue
	credentials[CredentialUpstreamSiteSessionExpiresAt] = expiresAt.Format(time.RFC3339)
}

func applyStoredSessionCookie(client *http.Client, baseURL, cookieValue string) bool {
	if client == nil || client.Jar == nil {
		return false
	}
	base, err := parseUpstreamBaseURL(baseURL)
	if err != nil {
		return false
	}
	name, value := splitStoredCookie(cookieValue)
	if name == "" || value == "" {
		return false
	}
	client.Jar.SetCookies(base, []*http.Cookie{{
		Name:  name,
		Value: value,
		Path:  "/",
	}})
	return true
}

func extractSessionCookie(headers http.Header) (string, time.Time) {
	now := time.Now().UTC()
	expiresAt := now.Add(upstreamSiteNewAPISessionTTL)
	for _, raw := range headers.Values("Set-Cookie") {
		cookie := parseSetCookieHeader(raw)
		if cookie == nil || !strings.EqualFold(cookie.Name, "session") || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		if cookie.MaxAge > 0 {
			expiresAt = now.Add(time.Duration(cookie.MaxAge) * time.Second)
		} else if !cookie.Expires.IsZero() {
			expiresAt = cookie.Expires.UTC()
		}
		return cookie.Name + "=" + cookie.Value, expiresAt
	}
	return "", time.Time{}
}

func parseSetCookieHeader(raw string) *http.Cookie {
	header := http.Header{}
	header.Add("Set-Cookie", raw)
	resp := http.Response{Header: header}
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return nil
	}
	return cookies[0]
}

func splitStoredCookie(raw string) (string, string) {
	first := strings.TrimSpace(strings.Split(raw, ";")[0])
	parts := strings.SplitN(first, "=", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func isUpstreamLoginStateAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "http 401") ||
		strings.Contains(text, "http 403") ||
		strings.Contains(text, "not logged") ||
		strings.Contains(text, "not login") ||
		strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "access token invalid") ||
		strings.Contains(text, "invalid access token")
}

func findUpstreamKeyRate(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput, token string) (*ResolveUpstreamKeyRateResult, error) {
	for page := 1; page <= input.MaxPages; page++ {
		keysURL, err := buildUpstreamConsoleURL(input.BaseURL, input.KeysPath, page, input.PageSize)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, keysURL, nil)
		if err != nil {
			return nil, infraerrors.BadRequest("UPSTREAM_RATE_KEYS_URL_INVALID", "keys URL is invalid").WithCause(err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		payload, statusCode, err := doUpstreamJSON(client, req)
		if err != nil {
			return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_KEYS_REQUEST_FAILED", "upstream keys request failed").WithCause(err)
		}
		if statusCode < 200 || statusCode >= 300 {
			return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_KEYS_FAILED", fmt.Sprintf("upstream keys request failed with HTTP %d", statusCode))
		}
		if err := assertUpstreamEnvelopeSuccess(payload, "UPSTREAM_RATE_KEYS_FAILED", "upstream keys request failed"); err != nil {
			return nil, err
		}

		items := extractUpstreamKeyItems(payload)
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			result, ok, err := matchUpstreamKeyRateItem(item, input.APIKey)
			if err != nil {
				return nil, err
			}
			if ok {
				return result, nil
			}
		}
	}

	return nil, infraerrors.NotFound(
		"UPSTREAM_RATE_KEY_NOT_FOUND",
		"upstream key was not found in the returned key list; the upstream may return masked keys or the page range may be too small",
	)
}

func fetchUpstreamAccountBalance(ctx context.Context, client *http.Client, baseURL, token string) (*float64, error) {
	profileURL, err := buildUpstreamConsoleURL(baseURL, upstreamKeyRateDefaultProfilePath, 0, 0)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, profileURL, nil)
	if err != nil {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_PROFILE_URL_INVALID", "profile URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	payload, statusCode, err := doUpstreamJSON(client, req)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_PROFILE_REQUEST_FAILED", "upstream profile request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_PROFILE_FAILED", fmt.Sprintf("upstream profile request failed with HTTP %d", statusCode))
	}
	if err := assertUpstreamEnvelopeSuccess(payload, "UPSTREAM_RATE_PROFILE_FAILED", "upstream profile request failed"); err != nil {
		return nil, err
	}
	return extractUpstreamAccountBalance(payload), nil
}

func loginNewAPIUpstream(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput) (*upstreamLoginResult, error) {
	if login := cachedNewAPIUpstreamLogin(client, input); login != nil {
		return login, nil
	}

	loginURL, err := buildUpstreamConsoleURL(input.BaseURL, upstreamKeyRateNewAPILoginPath, 0, 0)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{
		"username": input.Username,
		"password": input.Password,
	})
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_LOGIN_PAYLOAD_FAILED", "failed to encode upstream login payload").WithCause(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(body))
	if err != nil {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_LOGIN_URL_INVALID", "login URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	payload, statusCode, headers, err := doUpstreamJSONWithHeaders(client, req)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_LOGIN_REQUEST_FAILED", "upstream new-api login request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_LOGIN_FAILED", fmt.Sprintf("upstream new-api login failed with HTTP %d", statusCode))
	}
	if err := assertUpstreamSuccessBoolOrCode(payload, "UPSTREAM_RATE_LOGIN_FAILED", "upstream new-api login failed"); err != nil {
		return nil, err
	}
	login := &upstreamLoginResult{
		Token:   extractUpstreamAccessToken(payload),
		Balance: extractNewAPIAccountBalance(payload),
		UserID:  extractUpstreamUserID(payload),
	}
	rememberNewAPIUpstreamLogin(input.Credentials, input.BaseURL, login, headers)
	return login, nil
}

func findNewAPIUpstreamKeyRate(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput, token, userID string) (*ResolveUpstreamKeyRateResult, error) {
	matched, err := findNewAPIUpstreamMatchedToken(ctx, client, input, token, userID)
	if err != nil {
		return nil, err
	}
	return matched.Result, nil
}

func switchUpstreamKeyGroup(ctx context.Context, input ResolveUpstreamKeyRateInput, targetGroup string) (*ResolveUpstreamKeyRateResult, error) {
	normalized, err := normalizeResolveUpstreamKeyRateInput(input)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, siteType := range resolveUpstreamSiteTypeCandidates(normalized.SiteType) {
		var result *ResolveUpstreamKeyRateResult
		switch siteType {
		case UpstreamSiteTypeSub2API:
			result, err = switchSub2APIUpstreamKeyGroup(ctx, normalized, targetGroup)
		case UpstreamSiteTypeNewAPI:
			result, err = switchNewAPIUpstreamKeyGroup(ctx, normalized, targetGroup)
		default:
			err = infraerrors.BadRequest("UPSTREAM_RATE_SITE_TYPE_INVALID", "unsupported upstream site_type")
		}
		if err == nil {
			result.SiteType = siteType
			return result, nil
		}
		lastErr = err
	}
	if lastErr != nil && normalized.SiteType != UpstreamSiteTypeAuto {
		return nil, lastErr
	}
	if lastErr != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_GROUP_SWITCH_AUTO_DETECT_FAILED", "no supported upstream group switch endpoint succeeded").WithCause(lastErr)
	}
	return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_GROUP_SWITCH_AUTO_DETECT_FAILED", "no supported upstream group switch endpoint succeeded")
}

func switchSub2APIUpstreamKeyGroup(ctx context.Context, input ResolveUpstreamKeyRateInput, targetGroup string) (*ResolveUpstreamKeyRateResult, error) {
	normalized, err := normalizeResolveUpstreamKeyRateInput(input)
	if err != nil {
		return nil, err
	}
	targetGroup = strings.TrimSpace(targetGroup)
	if targetGroup == "" {
		return nil, infraerrors.BadRequest("UPSTREAM_GROUP_NAME_REQUIRED", "group_name is required")
	}

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:           upstreamKeyRateRequestTimeout,
		AllowPrivateHosts: true,
	})
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_HTTP_CLIENT_FAILED", "failed to create upstream HTTP client").WithCause(err)
	}

	login, err := loginUpstreamForAccess(ctx, client, normalized)
	if err != nil {
		return nil, err
	}
	if login.Balance == nil {
		if balance, balanceErr := fetchUpstreamAccountBalance(ctx, client, normalized.BaseURL, login.Token); balanceErr == nil {
			login.Balance = balance
		}
	}
	result, err := switchSub2APIUpstreamKeyGroupWithLogin(ctx, client, normalized, login, targetGroup)
	if err != nil && login.ReusedStoredState && isUpstreamLoginStateAuthFailure(err) {
		clearUpstreamSiteModeLoginState(normalized.Credentials)
		login, err = loginUpstreamForAccess(ctx, client, normalized)
		if err != nil {
			return nil, err
		}
		if login.Balance == nil {
			if balance, balanceErr := fetchUpstreamAccountBalance(ctx, client, normalized.BaseURL, login.Token); balanceErr == nil {
				login.Balance = balance
			}
		}
		result, err = switchSub2APIUpstreamKeyGroupWithLogin(ctx, client, normalized, login, targetGroup)
	}
	if err != nil {
		return nil, err
	}
	if login.Balance != nil {
		result.AccountBalance = login.Balance
	}
	return result, nil
}

func switchSub2APIUpstreamKeyGroupWithLogin(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput, login *upstreamLoginResult, targetGroup string) (*ResolveUpstreamKeyRateResult, error) {
	groups, err := fetchSub2APIGroupOptions(ctx, client, input.BaseURL, login.Token)
	if err != nil {
		return nil, err
	}
	target, ok := findUpstreamGroupOptionByName(groups, targetGroup)
	if !ok || target.ID == nil || *target.ID <= 0 {
		return nil, infraerrors.NotFound("UPSTREAM_GROUP_NOT_FOUND", "target upstream group was not found or does not expose an id")
	}
	item, matched, err := findSub2APIUpstreamMatchedKeyItem(ctx, client, input, login.Token)
	if err != nil {
		return nil, err
	}
	keyID, ok := int64Field(item, "id", "key_id", "keyId", "token_id", "tokenId")
	if !ok || keyID <= 0 {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_GROUP_SWITCH_KEY_ID_MISSING", "matched upstream key does not expose an id")
	}
	if err := updateSub2APIKeyGroup(ctx, client, input.BaseURL, login.Token, item, keyID, *target.ID); err != nil {
		return nil, err
	}

	result := &ResolveUpstreamKeyRateResult{
		RateMultiplier: target.RateMultiplier,
		GroupID:        target.ID,
		GroupName:      target.Name,
		KeyID:          &keyID,
		KeyName:        stringField(item, "name", "key_name", "keyName"),
		MatchedField:   "key",
	}
	if matched != nil {
		if matched.KeyName != "" {
			result.KeyName = matched.KeyName
		}
		if matched.MatchedField != "" {
			result.MatchedField = matched.MatchedField
		}
	}
	return result, nil
}

func switchNewAPIUpstreamKeyGroup(ctx context.Context, input ResolveUpstreamKeyRateInput, targetGroup string) (*ResolveUpstreamKeyRateResult, error) {
	normalized, err := normalizeResolveUpstreamKeyRateInput(input)
	if err != nil {
		return nil, err
	}
	targetGroup = strings.TrimSpace(targetGroup)
	if targetGroup == "" {
		return nil, infraerrors.BadRequest("UPSTREAM_GROUP_NAME_REQUIRED", "group_name is required")
	}

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:           upstreamKeyRateRequestTimeout,
		AllowPrivateHosts: true,
	})
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_HTTP_CLIENT_FAILED", "failed to create upstream HTTP client").WithCause(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_COOKIE_JAR_FAILED", "failed to create upstream cookie jar").WithCause(err)
	}
	client.Jar = jar

	login, err := loginNewAPIUpstream(ctx, client, normalized)
	if err != nil {
		return nil, err
	}
	if login.Balance == nil {
		if balance, balanceErr := fetchNewAPIAccountBalance(ctx, client, normalized.BaseURL, login.Token, login.UserID); balanceErr == nil {
			login.Balance = balance
		}
	}
	matched, err := findNewAPIUpstreamMatchedToken(ctx, client, normalized, login.Token, login.UserID)
	if err != nil {
		return nil, err
	}
	if err := updateNewAPITokenGroup(ctx, client, normalized.BaseURL, login.Token, login.UserID, matched.Item, matched.KeyID, targetGroup); err != nil {
		return nil, err
	}

	result, err := findNewAPIUpstreamKeyRate(ctx, client, normalized, login.Token, login.UserID)
	if err != nil {
		return nil, err
	}
	if login.Balance != nil {
		result.AccountBalance = login.Balance
	}
	return result, nil
}

func listUpstreamKeyGroups(ctx context.Context, input ResolveUpstreamKeyRateInput) ([]UpstreamKeyGroupOption, error) {
	normalized, err := normalizeResolveUpstreamKeyRateInput(input)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, siteType := range resolveUpstreamSiteTypeCandidates(normalized.SiteType) {
		switch siteType {
		case UpstreamSiteTypeSub2API:
			groups, err := listSub2APIUpstreamKeyGroups(ctx, normalized)
			if err == nil {
				return groups, nil
			}
			lastErr = err
		case UpstreamSiteTypeNewAPI:
			groups, err := listNewAPIUpstreamKeyGroups(ctx, normalized)
			if err == nil {
				return groups, nil
			}
			lastErr = err
		default:
			lastErr = infraerrors.BadRequest("UPSTREAM_RATE_SITE_TYPE_INVALID", "unsupported upstream site_type")
		}
	}
	if lastErr != nil && normalized.SiteType != UpstreamSiteTypeAuto {
		return nil, lastErr
	}
	if lastErr != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_GROUPS_AUTO_DETECT_FAILED", "no supported upstream group endpoint succeeded").WithCause(lastErr)
	}
	return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_GROUPS_AUTO_DETECT_FAILED", "no supported upstream group endpoint succeeded")
}

func listSub2APIUpstreamKeyGroups(ctx context.Context, input ResolveUpstreamKeyRateInput) ([]UpstreamKeyGroupOption, error) {
	normalized, err := normalizeResolveUpstreamKeyRateInput(input)
	if err != nil {
		return nil, err
	}

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:           upstreamKeyRateRequestTimeout,
		AllowPrivateHosts: true,
	})
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_HTTP_CLIENT_FAILED", "failed to create upstream HTTP client").WithCause(err)
	}
	login, err := loginUpstreamForAccess(ctx, client, normalized)
	if err != nil {
		return nil, err
	}
	groups, err := fetchSub2APIGroupOptions(ctx, client, normalized.BaseURL, login.Token)
	if err != nil && login.ReusedStoredState && isUpstreamLoginStateAuthFailure(err) {
		clearUpstreamSiteModeLoginState(normalized.Credentials)
		login, err = loginUpstreamForAccess(ctx, client, normalized)
		if err != nil {
			return nil, err
		}
		groups, err = fetchSub2APIGroupOptions(ctx, client, normalized.BaseURL, login.Token)
	}
	if err != nil {
		return nil, err
	}
	return groups, nil
}

func listNewAPIUpstreamKeyGroups(ctx context.Context, input ResolveUpstreamKeyRateInput) ([]UpstreamKeyGroupOption, error) {
	normalized, err := normalizeResolveUpstreamKeyRateInput(input)
	if err != nil {
		return nil, err
	}

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:           upstreamKeyRateRequestTimeout,
		AllowPrivateHosts: true,
	})
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_HTTP_CLIENT_FAILED", "failed to create upstream HTTP client").WithCause(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, infraerrors.InternalServer("UPSTREAM_RATE_COOKIE_JAR_FAILED", "failed to create upstream cookie jar").WithCause(err)
	}
	client.Jar = jar

	login, err := loginNewAPIUpstream(ctx, client, normalized)
	if err != nil {
		return nil, err
	}
	groups, err := fetchNewAPIUserGroupOptions(ctx, client, normalized.BaseURL, login.Token, login.UserID)
	if err != nil && login.ReusedStoredState && isUpstreamLoginStateAuthFailure(err) {
		clearUpstreamSiteModeLoginState(normalized.Credentials)
		login, err = loginNewAPIUpstream(ctx, client, normalized)
		if err != nil {
			return nil, err
		}
		groups, err = fetchNewAPIUserGroupOptions(ctx, client, normalized.BaseURL, login.Token, login.UserID)
	}
	if err != nil {
		return nil, err
	}
	return groups, nil
}

func findSub2APIUpstreamMatchedKeyItem(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput, token string) (map[string]any, *ResolveUpstreamKeyRateResult, error) {
	for page := 1; page <= input.MaxPages; page++ {
		keysURL, err := buildUpstreamConsoleURL(input.BaseURL, input.KeysPath, page, input.PageSize)
		if err != nil {
			return nil, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, keysURL, nil)
		if err != nil {
			return nil, nil, infraerrors.BadRequest("UPSTREAM_RATE_KEYS_URL_INVALID", "keys URL is invalid").WithCause(err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		payload, statusCode, err := doUpstreamJSON(client, req)
		if err != nil {
			return nil, nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_KEYS_REQUEST_FAILED", "upstream keys request failed").WithCause(err)
		}
		if statusCode < 200 || statusCode >= 300 {
			return nil, nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_KEYS_FAILED", fmt.Sprintf("upstream keys request failed with HTTP %d", statusCode))
		}
		if err := assertUpstreamEnvelopeSuccess(payload, "UPSTREAM_RATE_KEYS_FAILED", "upstream keys request failed"); err != nil {
			return nil, nil, err
		}

		items := extractUpstreamKeyItems(payload)
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			result, ok, err := matchUpstreamKeyRateItem(item, input.APIKey)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				return item, result, nil
			}
		}
	}

	return nil, nil, infraerrors.NotFound(
		"UPSTREAM_RATE_KEY_NOT_FOUND",
		"upstream key was not found in the returned key list; the upstream may return masked keys or the page range may be too small",
	)
}

func fetchSub2APIGroupOptions(ctx context.Context, client *http.Client, baseURL string, token string) ([]UpstreamKeyGroupOption, error) {
	groupsURL, err := buildUpstreamConsoleURL(baseURL, upstreamKeyRateDefaultGroupsPath, 0, 0)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(groupsURL)
	if err != nil {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_GROUPS_URL_INVALID", "sub2api groups URL is invalid").WithCause(err)
	}
	q := parsed.Query()
	if q.Get("timezone") == "" {
		q.Set("timezone", "Asia/Shanghai")
	}
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_GROUPS_URL_INVALID", "sub2api groups URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	payload, statusCode, err := doUpstreamJSON(client, req)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_GROUPS_REQUEST_FAILED", "upstream sub2api groups request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_GROUPS_FAILED", fmt.Sprintf("upstream sub2api groups request failed with HTTP %d", statusCode))
	}
	if err := assertUpstreamEnvelopeSuccess(payload, "UPSTREAM_RATE_GROUPS_FAILED", "upstream sub2api groups request failed"); err != nil {
		return nil, err
	}

	options := make([]UpstreamKeyGroupOption, 0)
	for _, item := range extractSub2APIGroupItems(payload) {
		option, ok := parseSub2APIGroupOption(item)
		if ok {
			options = append(options, option)
		}
	}
	sortUpstreamKeyGroupOptions(options)
	return options, nil
}

func extractSub2APIGroupItems(payload map[string]any) []map[string]any {
	if items := arrayMapsField(payload, "data", "items", "list", "groups", "records"); len(items) > 0 {
		return items
	}
	data, _ := mapField(payload, "data")
	if data == nil {
		return nil
	}
	if items := arrayMapsField(data, "items", "list", "groups", "records", "data"); len(items) > 0 {
		return items
	}
	items := make([]map[string]any, 0, len(data))
	for groupName, raw := range data {
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cloned := make(map[string]any, len(group)+1)
		for key, value := range group {
			cloned[key] = value
		}
		if stringField(cloned, "name", "group_name", "groupName") == "" {
			cloned["name"] = groupName
		}
		items = append(items, cloned)
	}
	return items
}

func parseSub2APIGroupOption(item map[string]any) (UpstreamKeyGroupOption, bool) {
	name := stringField(item, "name", "group_name", "groupName", "title")
	if name == "" {
		return UpstreamKeyGroupOption{}, false
	}
	rate, ok := numericField(item, "rate_multiplier", "rateMultiplier", "group_rate_multiplier", "groupRateMultiplier", "ratio", "group_ratio", "groupRatio")
	if !ok {
		if strings.EqualFold(name, "auto") || strings.EqualFold(name, "default") {
			rate, ok = 1, true
		}
	}
	if !ok {
		return UpstreamKeyGroupOption{}, false
	}
	option := UpstreamKeyGroupOption{
		Name:           name,
		RateMultiplier: rate,
		Description:    stringField(item, "description", "desc"),
	}
	if id, ok := int64Field(item, "id", "group_id", "groupId"); ok && id > 0 {
		option.ID = &id
	}
	return option, true
}

func findUpstreamGroupOptionByName(options []UpstreamKeyGroupOption, targetGroup string) (UpstreamKeyGroupOption, bool) {
	targetGroup = strings.TrimSpace(targetGroup)
	if targetGroup == "" {
		return UpstreamKeyGroupOption{}, false
	}
	for _, option := range options {
		if strings.TrimSpace(option.Name) == targetGroup {
			return option, true
		}
	}
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Name), targetGroup) {
			return option, true
		}
	}
	return UpstreamKeyGroupOption{}, false
}

func updateSub2APIKeyGroup(ctx context.Context, client *http.Client, baseURL string, token string, item map[string]any, keyID int64, targetGroupID int64) error {
	detail, err := fetchSub2APIKeyDetail(ctx, client, baseURL, token, keyID)
	if err != nil {
		return err
	}
	item = detail
	updateURL, err := buildUpstreamConsoleURL(baseURL, fmt.Sprintf("/api/v1/keys/%d", keyID), 0, 0)
	if err != nil {
		return err
	}
	body, err := json.Marshal(buildSub2APIKeyGroupUpdatePayload(item, targetGroupID))
	if err != nil {
		return infraerrors.InternalServer("UPSTREAM_GROUP_SWITCH_PAYLOAD_FAILED", "failed to encode sub2api key update payload").WithCause(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, updateURL, bytes.NewReader(body))
	if err != nil {
		return infraerrors.BadRequest("UPSTREAM_GROUP_SWITCH_URL_INVALID", "sub2api key update URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	payload, statusCode, err := doUpstreamJSON(client, req)
	if err != nil {
		if statusCode > 0 {
			return infraerrors.New(http.StatusBadGateway, "UPSTREAM_GROUP_SWITCH_FAILED", fmt.Sprintf("upstream sub2api key update returned non-JSON HTTP %d", statusCode)).WithCause(err)
		}
		return infraerrors.New(http.StatusBadGateway, "UPSTREAM_GROUP_SWITCH_REQUEST_FAILED", "upstream sub2api key update request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return infraerrors.New(http.StatusBadGateway, "UPSTREAM_GROUP_SWITCH_FAILED", fmt.Sprintf("upstream sub2api key update failed with HTTP %d", statusCode))
	}
	return assertUpstreamEnvelopeSuccess(payload, "UPSTREAM_GROUP_SWITCH_FAILED", "upstream sub2api key update failed")
}

func fetchSub2APIKeyDetail(ctx context.Context, client *http.Client, baseURL string, token string, keyID int64) (map[string]any, error) {
	detailURL, err := buildUpstreamConsoleURL(baseURL, fmt.Sprintf("/api/v1/keys/%d", keyID), 0, 0)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL, nil)
	if err != nil {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_KEY_DETAIL_URL_INVALID", "sub2api key detail URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	payload, statusCode, err := doUpstreamJSON(client, req)
	if err != nil {
		if statusCode > 0 {
			return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_KEY_DETAIL_FAILED", fmt.Sprintf("upstream sub2api key detail returned non-JSON HTTP %d", statusCode)).WithCause(err)
		}
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_KEY_DETAIL_REQUEST_FAILED", "upstream sub2api key detail request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_KEY_DETAIL_FAILED", fmt.Sprintf("upstream sub2api key detail request failed with HTTP %d", statusCode))
	}
	if err := assertUpstreamEnvelopeSuccess(payload, "UPSTREAM_RATE_KEY_DETAIL_FAILED", "upstream sub2api key detail request failed"); err != nil {
		return nil, err
	}
	if data, ok := mapField(payload, "data"); ok {
		return data, nil
	}
	return payload, nil
}

func buildSub2APIKeyGroupUpdatePayload(item map[string]any, targetGroupID int64) map[string]any {
	payload := map[string]any{"group_id": targetGroupID}
	if _, ok := rawField(item, "ip_whitelist", "ipWhitelist"); ok {
		payload["ip_whitelist"] = stringSliceField(item, "ip_whitelist", "ipWhitelist")
	}
	if _, ok := rawField(item, "ip_blacklist", "ipBlacklist"); ok {
		payload["ip_blacklist"] = stringSliceField(item, "ip_blacklist", "ipBlacklist")
	}
	return payload
}

func findNewAPIUpstreamMatchedToken(ctx context.Context, client *http.Client, input ResolveUpstreamKeyRateInput, token, userID string) (*newAPIUpstreamMatchedToken, error) {
	groupRatios, _, _ := fetchNewAPIUserGroupRatios(ctx, client, input.BaseURL, token, userID)
	for page := 1; page <= input.MaxPages; page++ {
		tokensURL, err := buildNewAPITokensURL(input.BaseURL, page, input.PageSize)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokensURL, nil)
		if err != nil {
			return nil, infraerrors.BadRequest("UPSTREAM_RATE_KEYS_URL_INVALID", "new-api token URL is invalid").WithCause(err)
		}
		req.Header.Set("Accept", "application/json")
		setOptionalBearerToken(req, token)
		setOptionalNewAPIUserHeader(req, userID)

		payload, statusCode, err := doUpstreamJSON(client, req)
		if err != nil {
			return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_KEYS_REQUEST_FAILED", "upstream new-api token request failed").WithCause(err)
		}
		if statusCode < 200 || statusCode >= 300 {
			return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_KEYS_FAILED", fmt.Sprintf("upstream new-api token request failed with HTTP %d", statusCode))
		}
		if err := assertUpstreamSuccessBoolOrCode(payload, "UPSTREAM_RATE_KEYS_FAILED", "upstream new-api token request failed"); err != nil {
			return nil, err
		}

		items := extractUpstreamKeyItems(payload)
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			keyID, ok := int64Field(item, "id", "key_id", "keyId", "token_id", "tokenId")
			if !ok || keyID <= 0 {
				continue
			}
			fullKey, err := fetchNewAPITokenKey(ctx, client, input.BaseURL, keyID, token, userID)
			if err != nil {
				return nil, err
			}
			if !sameComparableAPIKey(fullKey, input.APIKey) {
				continue
			}
			groupName := stringField(item, "group", "group_name", "groupName")
			rate, ok := groupRatios[groupName]
			if !ok {
				if v, hasInlineRate := numericField(item, "group_rate_multiplier", "groupRateMultiplier", "rate_multiplier", "rateMultiplier", "ratio"); hasInlineRate {
					rate, ok = v, true
				}
			}
			if !ok && strings.EqualFold(groupName, "auto") {
				rate, ok = 1, true
			}
			if !ok {
				return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_MULTIPLIER_MISSING", "matched new-api token group does not expose a numeric ratio")
			}
			result := &ResolveUpstreamKeyRateResult{
				RateMultiplier: rate,
				GroupName:      groupName,
				KeyID:          &keyID,
				KeyName:        stringField(item, "name", "key_name", "keyName"),
				MatchedField:   "token_key",
			}
			if balance := extractNewAPIAccountBalance(item); balance != nil {
				result.AccountBalance = balance
			}
			return &newAPIUpstreamMatchedToken{
				Item:   item,
				KeyID:  keyID,
				Result: result,
			}, nil
		}
	}
	return nil, infraerrors.NotFound("UPSTREAM_RATE_KEY_NOT_FOUND", "upstream new-api token was not found in the returned token list")
}

func updateNewAPITokenGroup(ctx context.Context, client *http.Client, baseURL string, token, userID string, item map[string]any, keyID int64, targetGroup string) error {
	updateURL, err := buildUpstreamConsoleURL(baseURL, upstreamKeyRateNewAPITokensPath, 0, 0)
	if err != nil {
		return err
	}
	body, err := json.Marshal(buildNewAPITokenGroupUpdatePayload(item, keyID, targetGroup))
	if err != nil {
		return infraerrors.InternalServer("UPSTREAM_GROUP_SWITCH_PAYLOAD_FAILED", "failed to encode new-api token update payload").WithCause(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, updateURL, bytes.NewReader(body))
	if err != nil {
		return infraerrors.BadRequest("UPSTREAM_GROUP_SWITCH_URL_INVALID", "new-api token update URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	setOptionalBearerToken(req, token)
	setOptionalNewAPIUserHeader(req, userID)
	payload, statusCode, err := doUpstreamJSON(client, req)
	if err != nil {
		return infraerrors.New(http.StatusBadGateway, "UPSTREAM_GROUP_SWITCH_REQUEST_FAILED", "upstream new-api token update request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return infraerrors.New(http.StatusBadGateway, "UPSTREAM_GROUP_SWITCH_FAILED", fmt.Sprintf("upstream new-api token update failed with HTTP %d", statusCode))
	}
	if err := assertUpstreamSuccessBoolOrCode(payload, "UPSTREAM_GROUP_SWITCH_FAILED", "upstream new-api token update failed"); err != nil {
		return err
	}
	return nil
}

func buildNewAPITokenGroupUpdatePayload(item map[string]any, keyID int64, targetGroup string) map[string]any {
	payload := map[string]any{
		"id":                   keyID,
		"name":                 stringField(item, "name", "key_name", "keyName"),
		"remain_quota":         rawFieldOrDefault(item, 0, "remain_quota", "remainQuota"),
		"expired_time":         rawFieldOrDefault(item, -1, "expired_time", "expiredTime"),
		"unlimited_quota":      boolFieldOrDefault(item, false, "unlimited_quota", "unlimitedQuota"),
		"model_limits_enabled": boolFieldOrDefault(item, false, "model_limits_enabled", "modelLimitsEnabled"),
		"model_limits":         stringField(item, "model_limits", "modelLimits"),
		"allow_ips":            stringField(item, "allow_ips", "allowIps"),
		"group":                strings.TrimSpace(targetGroup),
		"cross_group_retry":    boolFieldOrDefault(item, false, "cross_group_retry", "crossGroupRetry"),
	}
	if status, ok := rawField(item, "status"); ok {
		payload["status"] = status
	}
	return payload
}

func buildNewAPITokensURL(baseURL string, page, pageSize int) (string, error) {
	tokensURL, err := buildUpstreamConsoleURL(baseURL, upstreamKeyRateNewAPITokensPath, 0, 0)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(tokensURL)
	if err != nil {
		return "", infraerrors.BadRequest("UPSTREAM_RATE_KEYS_URL_INVALID", "new-api token URL is invalid").WithCause(err)
	}
	q := parsed.Query()
	if q.Get("p") == "" {
		q.Set("p", strconv.Itoa(page))
	}
	if q.Get("page_size") == "" {
		q.Set("page_size", strconv.Itoa(pageSize))
	}
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func fetchNewAPIUserGroupRatios(ctx context.Context, client *http.Client, baseURL string, token, userID string) (map[string]float64, map[string]string, error) {
	options, err := fetchNewAPIUserGroupOptions(ctx, client, baseURL, token, userID)
	if err != nil {
		return nil, nil, err
	}
	ratios := make(map[string]float64, len(options))
	descs := make(map[string]string, len(options))
	for _, option := range options {
		ratios[option.Name] = option.RateMultiplier
		if option.Description != "" {
			descs[option.Name] = option.Description
		}
	}
	return ratios, descs, nil
}

func fetchNewAPIUserGroupOptions(ctx context.Context, client *http.Client, baseURL string, token, userID string) ([]UpstreamKeyGroupOption, error) {
	groupsURL, err := buildUpstreamConsoleURL(baseURL, upstreamKeyRateNewAPIGroupsPath, 0, 0)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, groupsURL, nil)
	if err != nil {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_GROUPS_URL_INVALID", "new-api groups URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	setOptionalBearerToken(req, token)
	setOptionalNewAPIUserHeader(req, userID)
	payload, statusCode, err := doUpstreamJSON(client, req)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_GROUPS_REQUEST_FAILED", "upstream new-api groups request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_GROUPS_FAILED", fmt.Sprintf("upstream new-api groups request failed with HTTP %d", statusCode))
	}
	if err := assertUpstreamSuccessBoolOrCode(payload, "UPSTREAM_RATE_GROUPS_FAILED", "upstream new-api groups request failed"); err != nil {
		return nil, err
	}

	data, _ := mapField(payload, "data")
	options := make([]UpstreamKeyGroupOption, 0, len(data))
	for groupName, raw := range data {
		groupName = strings.TrimSpace(groupName)
		if groupName == "" {
			continue
		}
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ratio, ok := numericField(group, "ratio", "rate_multiplier", "rateMultiplier", "group_rate_multiplier", "groupRateMultiplier")
		if !ok {
			if strings.EqualFold(groupName, "auto") {
				ratio, ok = 1, true
			}
		}
		if !ok {
			continue
		}
		options = append(options, UpstreamKeyGroupOption{
			Name:           groupName,
			RateMultiplier: ratio,
			Description:    stringField(group, "desc", "description", "name"),
		})
	}
	sortUpstreamKeyGroupOptions(options)
	return options, nil
}

func fetchNewAPIAccountBalance(ctx context.Context, client *http.Client, baseURL string, token, userID string) (*float64, error) {
	selfURL, err := buildUpstreamConsoleURL(baseURL, upstreamKeyRateNewAPISelfPath, 0, 0)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, selfURL, nil)
	if err != nil {
		return nil, infraerrors.BadRequest("UPSTREAM_RATE_SELF_URL_INVALID", "new-api self URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	setOptionalBearerToken(req, token)
	setOptionalNewAPIUserHeader(req, userID)
	payload, statusCode, err := doUpstreamJSON(client, req)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_SELF_REQUEST_FAILED", "upstream new-api self request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_SELF_FAILED", fmt.Sprintf("upstream new-api self request failed with HTTP %d", statusCode))
	}
	if err := assertUpstreamSuccessBoolOrCode(payload, "UPSTREAM_RATE_SELF_FAILED", "upstream new-api self request failed"); err != nil {
		return nil, err
	}
	return extractNewAPIAccountBalance(payload), nil
}

func fetchNewAPITokenKey(ctx context.Context, client *http.Client, baseURL string, keyID int64, token, userID string) (string, error) {
	keyURL, err := buildUpstreamConsoleURL(baseURL, fmt.Sprintf("/api/token/%d/key", keyID), 0, 0)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, keyURL, nil)
	if err != nil {
		return "", infraerrors.BadRequest("UPSTREAM_RATE_TOKEN_KEY_URL_INVALID", "new-api token key URL is invalid").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	setOptionalBearerToken(req, token)
	setOptionalNewAPIUserHeader(req, userID)
	payload, statusCode, err := doUpstreamJSON(client, req)
	if err != nil {
		return "", infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_TOKEN_KEY_REQUEST_FAILED", "upstream new-api token key request failed").WithCause(err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return "", infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_TOKEN_KEY_FAILED", fmt.Sprintf("upstream new-api token key request failed with HTTP %d", statusCode))
	}
	if err := assertUpstreamSuccessBoolOrCode(payload, "UPSTREAM_RATE_TOKEN_KEY_FAILED", "upstream new-api token key request failed"); err != nil {
		return "", err
	}
	if key := stringField(payload, "key"); key != "" {
		return key, nil
	}
	data, _ := mapField(payload, "data")
	if data != nil {
		if key := stringField(data, "key"); key != "" {
			return key, nil
		}
	}
	return "", infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_TOKEN_KEY_MISSING", "new-api token key response did not contain key")
}

func setOptionalBearerToken(req *http.Request, token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
}

func setOptionalNewAPIUserHeader(req *http.Request, userID string) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return
	}
	req.Header.Set("New-Api-User", userID)
}

func buildUpstreamConsoleURL(baseURL, pathValue string, page, pageSize int) (string, error) {
	base, err := parseUpstreamBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	parsedPath, err := url.Parse(pathValue)
	if err != nil {
		return "", infraerrors.BadRequest("UPSTREAM_RATE_PATH_INVALID", "upstream path is invalid").WithCause(err)
	}
	if parsedPath.IsAbs() {
		if !strings.EqualFold(parsedPath.Scheme, base.Scheme) || !strings.EqualFold(parsedPath.Host, base.Host) {
			return "", infraerrors.BadRequest("UPSTREAM_RATE_PATH_INVALID", "upstream path must stay under base_url host")
		}
		base.Path = strings.TrimRight(parsedPath.Path, "/")
		base.RawQuery = parsedPath.RawQuery
	} else {
		pathPart := strings.TrimSpace(parsedPath.Path)
		if pathPart == "" {
			pathPart = "/"
		}
		if !strings.HasPrefix(pathPart, "/") {
			pathPart = "/" + pathPart
		}
		base.Path = strings.TrimRight(base.Path, "/") + pathPart
		base.RawQuery = parsedPath.RawQuery
	}
	if page > 0 {
		q := base.Query()
		if q.Get("page") == "" {
			q.Set("page", strconv.Itoa(page))
		}
		if q.Get("page_size") == "" {
			q.Set("page_size", strconv.Itoa(pageSize))
		}
		if q.Get("sort_by") == "" {
			q.Set("sort_by", "created_at")
		}
		if q.Get("sort_order") == "" {
			q.Set("sort_order", "desc")
		}
		base.RawQuery = q.Encode()
	}
	return base.String(), nil
}

func doUpstreamJSON(client *http.Client, req *http.Request) (map[string]any, int, error) {
	payload, statusCode, _, err := doUpstreamJSONWithHeaders(client, req)
	return payload, statusCode, err
}

func doUpstreamJSONWithHeaders(client *http.Client, req *http.Request) (map[string]any, int, http.Header, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamKeyRateMaxBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, resp.Header.Clone(), err
	}
	var payload map[string]any
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&payload); err != nil {
		return nil, resp.StatusCode, resp.Header.Clone(), fmt.Errorf("upstream returned non-JSON response: status=%d content_type=%q: %w", resp.StatusCode, resp.Header.Get("Content-Type"), err)
	}
	return payload, resp.StatusCode, resp.Header.Clone(), nil
}

func assertUpstreamEnvelopeSuccess(payload map[string]any, reason, fallbackMessage string) error {
	code, ok := numericField(payload, "code")
	if !ok || code == 0 {
		return nil
	}
	message := stringField(payload, "message", "msg", "error")
	if message == "" {
		message = fallbackMessage
	}
	return infraerrors.New(http.StatusBadGateway, reason, message)
}

func assertUpstreamSuccessBoolOrCode(payload map[string]any, reason, fallbackMessage string) error {
	if successRaw, ok := payload["success"]; ok {
		if success, ok := successRaw.(bool); ok && !success {
			message := stringField(payload, "message", "msg", "error")
			if message == "" {
				message = fallbackMessage
			}
			return infraerrors.New(http.StatusBadGateway, reason, message)
		}
	}
	return assertUpstreamEnvelopeSuccess(payload, reason, fallbackMessage)
}

func extractUpstreamAccessToken(payload map[string]any) string {
	if token := stringField(payload, "access_token", "accessToken", "token"); token != "" {
		return token
	}
	data, _ := mapField(payload, "data")
	if data == nil {
		return ""
	}
	return stringField(data, "access_token", "accessToken", "token")
}

func extractUpstreamRefreshToken(payload map[string]any) string {
	if token := stringField(payload, "refresh_token", "refreshToken"); token != "" {
		return token
	}
	data, _ := mapField(payload, "data")
	if data == nil {
		return ""
	}
	return stringField(data, "refresh_token", "refreshToken")
}

func extractUpstreamExpiresIn(payload map[string]any) int {
	if expiresIn, ok := numericField(payload, "expires_in", "expiresIn"); ok && expiresIn > 0 {
		return int(expiresIn)
	}
	data, _ := mapField(payload, "data")
	if data == nil {
		return 0
	}
	if expiresIn, ok := numericField(data, "expires_in", "expiresIn"); ok && expiresIn > 0 {
		return int(expiresIn)
	}
	return 0
}

func extractUpstreamAccountBalance(payload map[string]any) *float64 {
	if balance, ok := numericField(payload, "balance", "quota", "credit", "credits", "amount"); ok {
		return &balance
	}
	data, _ := mapField(payload, "data")
	if data == nil {
		return nil
	}
	if balance, ok := numericField(data, "balance", "quota", "credit", "credits", "amount"); ok {
		return &balance
	}
	user, _ := mapField(data, "user")
	if user != nil {
		if balance, ok := numericField(user, "balance", "quota", "credit", "credits", "amount"); ok {
			return &balance
		}
	}
	user, _ = mapField(payload, "user")
	if user != nil {
		if balance, ok := numericField(user, "balance", "quota", "credit", "credits", "amount"); ok {
			return &balance
		}
	}
	return nil
}

func extractNewAPIAccountBalance(payload map[string]any) *float64 {
	if quota, ok := extractNumericFromPayload(payload, []string{"quota", "remain_quota", "remainQuota"}); ok {
		balance := quota / upstreamKeyRateNewAPIQuotaPerUSD
		return &balance
	}
	if balance, ok := extractNumericFromPayload(payload, []string{"balance", "credit", "credits", "amount"}); ok {
		return &balance
	}
	return nil
}

func extractNumericFromPayload(payload map[string]any, names []string) (float64, bool) {
	if value, ok := numericField(payload, names...); ok {
		return value, true
	}
	data, _ := mapField(payload, "data")
	if data != nil {
		if value, ok := numericField(data, names...); ok {
			return value, true
		}
		user, _ := mapField(data, "user")
		if user != nil {
			if value, ok := numericField(user, names...); ok {
				return value, true
			}
		}
	}
	user, _ := mapField(payload, "user")
	if user != nil {
		if value, ok := numericField(user, names...); ok {
			return value, true
		}
	}
	return 0, false
}

func extractUpstreamUserID(payload map[string]any) string {
	if id, ok := int64Field(payload, "id", "user_id", "userId"); ok && id > 0 {
		return strconv.FormatInt(id, 10)
	}
	if id := stringField(payload, "id", "user_id", "userId"); id != "" {
		return id
	}
	data, _ := mapField(payload, "data")
	if data == nil {
		return ""
	}
	if id, ok := int64Field(data, "id", "user_id", "userId"); ok && id > 0 {
		return strconv.FormatInt(id, 10)
	}
	if id := stringField(data, "id", "user_id", "userId"); id != "" {
		return id
	}
	user, _ := mapField(data, "user")
	if user != nil {
		if id, ok := int64Field(user, "id", "user_id", "userId"); ok && id > 0 {
			return strconv.FormatInt(id, 10)
		}
		if id := stringField(user, "id", "user_id", "userId"); id != "" {
			return id
		}
	}
	user, _ = mapField(payload, "user")
	if user != nil {
		if id, ok := int64Field(user, "id", "user_id", "userId"); ok && id > 0 {
			return strconv.FormatInt(id, 10)
		}
		if id := stringField(user, "id", "user_id", "userId"); id != "" {
			return id
		}
	}
	return ""
}

func extractUpstreamKeyItems(payload map[string]any) []map[string]any {
	if items := arrayMapsField(payload, "items", "list", "keys", "tokens"); len(items) > 0 {
		return items
	}
	if dataItems := arrayMapsField(payload, "data"); len(dataItems) > 0 {
		return dataItems
	}
	data, _ := mapField(payload, "data")
	if data == nil {
		return nil
	}
	if items := arrayMapsField(data, "items", "list", "keys", "tokens", "records"); len(items) > 0 {
		return items
	}
	if nestedDataItems := arrayMapsField(data, "data"); len(nestedDataItems) > 0 {
		return nestedDataItems
	}
	nestedData, _ := mapField(data, "data")
	if nestedData == nil {
		return nil
	}
	return arrayMapsField(nestedData, "items", "list", "keys", "tokens", "records")
}

func matchUpstreamKeyRateItem(item map[string]any, targetAPIKey string) (*ResolveUpstreamKeyRateResult, bool, error) {
	for _, field := range []string{"key", "api_key", "apiKey", "token", "value", "sk", "key_value", "keyValue"} {
		candidate := stringField(item, field)
		if !sameComparableAPIKey(candidate, targetAPIKey) {
			continue
		}
		result, err := extractUpstreamKeyRateResult(item)
		if err != nil {
			return nil, false, err
		}
		result.MatchedField = field
		return result, true, nil
	}
	return nil, false, nil
}

func extractUpstreamKeyRateResult(item map[string]any) (*ResolveUpstreamKeyRateResult, error) {
	group, _ := mapField(item, "group")
	rate, ok := numericField(item, "group_rate_multiplier", "groupRateMultiplier", "rate_multiplier", "rateMultiplier")
	if !ok && group != nil {
		rate, ok = numericField(group, "rate_multiplier", "rateMultiplier", "group_rate_multiplier", "groupRateMultiplier", "ratio", "group_ratio", "groupRatio")
	}
	if !ok {
		return nil, infraerrors.New(http.StatusBadGateway, "UPSTREAM_RATE_MULTIPLIER_MISSING", "matched upstream key does not contain group rate_multiplier")
	}

	result := &ResolveUpstreamKeyRateResult{
		RateMultiplier: rate,
		KeyName:        stringField(item, "name", "key_name", "keyName"),
	}
	if keyID, ok := int64Field(item, "id", "key_id", "keyId", "token_id", "tokenId"); ok {
		result.KeyID = &keyID
	}
	if group != nil {
		result.GroupName = stringField(group, "name", "group_name", "groupName")
		if groupID, ok := int64Field(group, "id", "group_id", "groupId"); ok {
			result.GroupID = &groupID
		}
	}
	if result.GroupName == "" {
		result.GroupName = stringField(item, "group_name", "groupName")
	}
	if result.GroupID == nil {
		if groupID, ok := int64Field(item, "group_id", "groupId"); ok {
			result.GroupID = &groupID
		}
	}
	return result, nil
}

func normalizeComparableAPIKey(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "Bearer ")
	value = strings.TrimPrefix(value, "bearer ")
	return strings.TrimSpace(value)
}

func sameComparableAPIKey(candidate, target string) bool {
	candidate = normalizeComparableAPIKey(candidate)
	target = normalizeComparableAPIKey(target)
	if candidate == "" || target == "" {
		return false
	}
	if candidate == target {
		return true
	}
	return strings.TrimPrefix(candidate, "sk-") == strings.TrimPrefix(target, "sk-")
}

func stringField(m map[string]any, names ...string) string {
	for _, name := range names {
		raw, ok := m[name]
		if !ok || raw == nil {
			continue
		}
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func rawField(m map[string]any, names ...string) (any, bool) {
	for _, name := range names {
		raw, ok := m[name]
		if ok && raw != nil {
			return raw, true
		}
	}
	return nil, false
}

func rawFieldOrDefault(m map[string]any, fallback any, names ...string) any {
	if value, ok := rawField(m, names...); ok {
		return value
	}
	return fallback
}

func boolFieldOrDefault(m map[string]any, fallback bool, names ...string) bool {
	for _, name := range names {
		raw, ok := m[name]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case bool:
			return v
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "1", "yes", "on":
				return true
			case "false", "0", "no", "off":
				return false
			}
		case float64:
			return v != 0
		case int:
			return v != 0
		case int64:
			return v != 0
		case json.Number:
			n, err := v.Int64()
			if err == nil {
				return n != 0
			}
		}
	}
	return fallback
}

func mapField(m map[string]any, name string) (map[string]any, bool) {
	raw, ok := m[name]
	if !ok || raw == nil {
		return nil, false
	}
	if typed, ok := raw.(map[string]any); ok {
		return typed, true
	}
	return nil, false
}

func arrayMapsField(m map[string]any, names ...string) []map[string]any {
	for _, name := range names {
		raw, ok := m[name]
		if !ok || raw == nil {
			continue
		}
		values, ok := raw.([]any)
		if !ok {
			continue
		}
		items := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if item, ok := value.(map[string]any); ok {
				items = append(items, item)
			}
		}
		return items
	}
	return nil
}

func stringSliceField(m map[string]any, names ...string) []string {
	for _, name := range names {
		raw, ok := m[name]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case []string:
			return append([]string(nil), v...)
		case []any:
			values := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					values = append(values, strings.TrimSpace(s))
				}
			}
			return values
		case string:
			v = strings.TrimSpace(v)
			if v == "" {
				return nil
			}
			values := strings.Split(v, ",")
			for i := range values {
				values[i] = strings.TrimSpace(values[i])
			}
			return values
		}
	}
	return nil
}

func numericField(m map[string]any, names ...string) (float64, bool) {
	for _, name := range names {
		raw, ok := m[name]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case float64:
			return v, true
		case float32:
			return float64(v), true
		case int:
			return float64(v), true
		case int64:
			return float64(v), true
		case json.Number:
			n, err := v.Float64()
			return n, err == nil
		case string:
			n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func sortUpstreamKeyGroupOptions(options []UpstreamKeyGroupOption) {
	sort.SliceStable(options, func(i, j int) bool {
		if options[i].RateMultiplier == options[j].RateMultiplier {
			return strings.ToLower(options[i].Name) < strings.ToLower(options[j].Name)
		}
		return options[i].RateMultiplier < options[j].RateMultiplier
	})
}

func int64Field(m map[string]any, names ...string) (int64, bool) {
	for _, name := range names {
		raw, ok := m[name]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case float64:
			return int64(v), true
		case int:
			return int64(v), true
		case int64:
			return v, true
		case json.Number:
			n, err := v.Int64()
			return n, err == nil
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err == nil {
				return n, true
			}
		}
	}
	return 0, false
}
