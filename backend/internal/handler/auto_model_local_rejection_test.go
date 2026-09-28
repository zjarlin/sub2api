//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func autoModelLocalRejectionForTest() *service.UpstreamFailoverError {
	return &service.UpstreamFailoverError{
		StatusCode:                 http.StatusServiceUnavailable,
		Stage:                      service.GatewayFailureStageInference,
		Scope:                      service.GatewayFailureScopeRequest,
		Reason:                     service.AutoModelExcludedReason,
		ClientStatusCode:           http.StatusServiceUnavailable,
		ClientMessage:              "Auto model routing excludes the selected upstream model",
		SkipAccountScheduleFailure: true,
	}
}

func TestAutoModelLocalRejectionPreservesAccountSwitchBudget(t *testing.T) {
	account := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}
	for _, replayable := range []bool{false, true} {
		t.Run(fmt.Sprintf("replayable_%t", replayable), func(t *testing.T) {
			budget := openAIAccountSwitchBudget{limit: 0, replayable: replayable}
			for range 10 {
				require.False(t, budget.exhausted(account, autoModelLocalRejectionForTest()))
			}
			require.Zero(t, budget.failures)
			require.False(t, budget.requireCompatible)
			ordinaryRequestFailure := autoModelLocalRejectionForTest()
			ordinaryRequestFailure.Reason = "ordinary_request_failure"
			require.True(t, budget.exhausted(account, ordinaryRequestFailure))
		})
	}

	for _, tc := range []struct {
		name   string
		reason service.GatewayFailureReason
		scope  service.GatewayFailureScope
		skip   bool
	}{
		{name: "different reason", reason: "ordinary_request_failure", scope: service.GatewayFailureScopeRequest, skip: true},
		{name: "account scope", reason: service.AutoModelExcludedReason, scope: service.GatewayFailureScopeAccount, skip: true},
		{name: "provider scope", reason: service.AutoModelExcludedReason, scope: service.GatewayFailureScopeProvider, skip: true},
		{name: "schedule failure enabled", reason: service.AutoModelExcludedReason, scope: service.GatewayFailureScopeRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := autoModelLocalRejectionForTest()
			failure.Reason, failure.Scope, failure.SkipAccountScheduleFailure = tc.reason, tc.scope, tc.skip
			budget := openAIAccountSwitchBudget{limit: 0}
			require.True(t, budget.exhausted(account, failure))
		})
	}
}

type autoModelLocalCompactUpstream struct {
	service.HTTPUpstream
	accountIDs []int64
	models     []string
}

func (u *autoModelLocalCompactUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	model := gjson.GetBytes(body, "model").String()
	u.accountIDs = append(u.accountIDs, accountID)
	u.models = append(u.models, model)
	payload, err := json.Marshal(map[string]any{
		"id": "resp_compact_test", "object": "response.compaction", "status": "completed", "model": model,
		"output": []map[string]string{{"id": "cmp_test", "type": "compaction", "encrypted_content": "cipher"}},
		"usage":  map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
	})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(payload)),
	}, nil
}

func TestAutoModelLocalRejectionCompactTriesAnotherAccountWithZeroSwitchBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &grokCredentialHandlerRepo{}
	for id := int64(1); id <= 2; id++ {
		credentials := map[string]any{
			"api_key":       "test-only",
			"base_url":      "https://upstream.example",
			"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"},
		}
		if id == 2 {
			credentials["compact_model_mapping"] = map[string]any{"gpt-5.5": "gpt-5.5"}
		}
		repo.accounts = append(repo.accounts, service.Account{
			ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: int(id),
			Credentials: credentials, Extra: map[string]any{"openai_compact_supported": true},
		})
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.OpenAICompactModel = "gpt-6-astra"
	settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
		service.SettingKeyModelFallbackPolicy: `{"enabled":false,"tiers":[{"name":"highest","models":["gpt-6-astra"]},{"name":"standard","models":["gpt-5.5"]}]}`,
	}}, cfg)
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	require.False(t, service.AutoModelAllowed(ctx, "gpt-6-astra"))
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()
	upstream := &autoModelLocalCompactUpstream{}
	var selectedAccountIDs []int64
	cache := &concurrencyCacheMock{
		acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(_ context.Context, id int64, _ int, _ string) (bool, error) {
			selectedAccountIDs = append(selectedAccountIDs, id)
			return true, nil
		},
	}
	concurrency := service.NewConcurrencyService(cache)
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, concurrency,
		service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
	h.maxAccountSwitches = 0
	key := &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
	})
	var requestContext *gin.Context
	router.POST("/v1/responses/compact", func(c *gin.Context) {
		h.Responses(c)
		requestContext = c
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"model":"gpt-5.5","input":"hi","stream":false}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, "response=%s selected=%v upstream=%v models=%v", recorder.Body.String(), selectedAccountIDs, upstream.accountIDs, upstream.models)
	require.Equal(t, []int64{1, 2}, selectedAccountIDs)
	require.Equal(t, []int64{2}, upstream.accountIDs)
	require.Equal(t, []string{"gpt-5.5"}, upstream.models)
	require.Equal(t, "compaction", gjson.GetBytes(recorder.Body.Bytes(), "output.0.type").String())
	require.Equal(t, "cipher", gjson.GetBytes(recorder.Body.Bytes(), "output.0.encrypted_content").String())
	require.Empty(t, recorder.Header().Get("X-Sub2api-Fallback-Model"))
	require.Empty(t, repo.setErrorIDs)
	require.Empty(t, repo.setTempIDs)
	require.Empty(t, repo.rateLimitIDs)
	require.NotNil(t, requestContext)
	attempts, _ := requestContext.Get(service.OpsUpstreamErrorsKey)
	require.Empty(t, attempts)
}

type autoModelGrok429Upstream struct {
	service.HTTPUpstream
	accountIDs []int64
	models     []string
}

func (u *autoModelGrok429Upstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.accountIDs = append(u.accountIDs, accountID)
	model := gjson.GetBytes(body, "model").String()
	u.models = append(u.models, model)
	if accountID != 1 {
		payload, err := json.Marshal(map[string]any{
			"id": "resp_grok_test", "object": "response", "status": "completed", "model": model,
			"output": []map[string]any{{
				"id": "msg_grok_test", "type": "message", "role": "assistant", "status": "completed",
				"content": []map[string]string{{"type": "output_text", "text": "ok"}},
			}},
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
		})
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(payload)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Content-Type": {"application/json"},
			"Retry-After":  {"60"},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}, nil
}

func TestAutoModelLocalRejectionDoesNotConsumeGrokOAuth429Followup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{ID: 71, Platform: service.PlatformComposite, Status: service.StatusActive}
	repo := &grokCredentialHandlerRepo{accounts: []service.Account{
		{
			ID: 1, Name: "grok-oauth", Platform: service.PlatformGrok, Type: service.AccountTypeOAuth,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
			Credentials: map[string]any{
				"access_token": "test-only", "refresh_token": "test-only", "expires_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
				"model_mapping": map[string]any{"grok-4.6": "grok-4.6"},
			},
		},
		{
			ID: 2, Name: "grok-alias", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2,
			Credentials: map[string]any{
				"api_key":       "test-only",
				"model_mapping": map[string]any{"grok-4.6": "grok"},
			},
		},
		{
			ID: 3, Name: "grok-low-tier", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 3,
			Credentials: map[string]any{
				"api_key":       "test-only",
				"model_mapping": map[string]any{"grok-4.6": "grok-4.6"},
			},
		},
	}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
		service.SettingKeyModelFallbackPolicy: `{"enabled":false,"tiers":[{"name":"highest","models":["grok-4.5"]},{"name":"standard","models":["grok-4.6"]}]}`,
	}}, cfg)
	ctx, err := settings.BindAutoModelRoutingPolicy(context.Background())
	require.NoError(t, err)
	ctx = service.WithResolvedTargetPlatform(ctx, service.PlatformGrok)
	require.True(t, service.AutoModelAllowed(ctx, "grok"))
	require.False(t, service.AutoModelAllowed(ctx, "grok-4.5"))
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()
	upstream := &autoModelGrok429Upstream{}
	var selectedAccountIDs []int64
	cache := &concurrencyCacheMock{
		acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(_ context.Context, accountID int64, _ int, _ string) (bool, error) {
			selectedAccountIDs = append(selectedAccountIDs, accountID)
			return true, nil
		},
	}
	concurrency := service.NewConcurrencyService(cache)
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, concurrency,
		service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil,
		service.NewGrokTokenProvider(repo, nil), nil, nil, nil, settings, nil)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
	h.maxAccountSwitches = 1
	key := &service.APIKey{ID: 2, GroupID: &group.ID, Group: group, User: &service.User{ID: 3, Status: service.StatusActive}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.User.ID, Concurrency: 1})
	})
	var requestContext *gin.Context
	router.POST("/v1/responses", func(c *gin.Context) {
		h.Responses(c)
		requestContext = c
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"grok-4.6","input":"hi","stream":false}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, "response=%s selected=%v outbound=%v models=%v", recorder.Body.String(), selectedAccountIDs, upstream.accountIDs, upstream.models)
	require.Equal(t, []int64{1, 2, 3}, selectedAccountIDs)
	require.Equal(t, []int64{1, 3}, upstream.accountIDs)
	require.Equal(t, []string{"grok-4.6", "grok-4.6"}, upstream.models)
	require.NotContains(t, upstream.models, "grok-4.5")
	require.Equal(t, "completed", gjson.GetBytes(recorder.Body.Bytes(), "status").String())
	require.Equal(t, "grok-4.6", gjson.GetBytes(recorder.Body.Bytes(), "model").String())
	require.Equal(t, []int64{1}, repo.rateLimitedAccountIDs())
	require.Empty(t, repo.setErrorIDs)
	require.Empty(t, repo.setTempIDs)
	require.NotNil(t, requestContext)
	value, _ := requestContext.Get(service.OpsUpstreamErrorsKey)
	events, ok := value.([]*service.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, int64(1), events[0].AccountID)
	require.Equal(t, http.StatusTooManyRequests, events[0].UpstreamStatusCode)
}

func TestAutoModelLocalRejectionExhaustionKeepsLocalOpsAttribution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name          string
		streamStarted bool
		priorFailure  bool
	}{
		{name: "JSON"},
		{name: "JSON with earlier upstream failure", priorFailure: true},
		{name: "SSE", streamStarted: true},
		{name: "SSE with earlier upstream failure", streamStarted: true, priorFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			streamStarted, priorFailure := tc.streamStarted, tc.priorFailure
			setupOpsErrorLogTestQueue(t, 4)
			ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			h := &OpenAIGatewayHandler{}
			rejection := autoModelLocalRejectionForTest()
			priorEvents := []*service.OpsUpstreamErrorEvent{{
				Platform: service.PlatformGrok, AccountID: 41, AccountName: "prior-provider", Model: "grok-4.5",
				Stage: string(service.GatewayFailureStageInference), Kind: "http_error",
				UpstreamStatusCode: http.StatusTooManyRequests, Message: "earlier real upstream rate limit",
			}}
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			var requestContext *gin.Context
			router.POST("/v1/responses", func(c *gin.Context) {
				setOpsRequestContext(c, "gpt-5.5", streamStarted)
				setOpsSelectedAccount(c, 42, service.PlatformOpenAI)
				service.SetOpsUpstreamModel(c, "gpt-6-astra")
				service.SetActualOpenAIUpstreamEndpoint(c, EndpointResponses)
				if priorFailure {
					c.Set(service.OpsUpstreamErrorsKey, priorEvents)
					service.SetOpsUpstreamError(c, http.StatusTooManyRequests, priorEvents[0].Message, "")
				}
				if streamStarted {
					c.Header("Content-Type", "text/event-stream")
					c.Status(http.StatusOK)
					c.Writer.WriteHeaderNow()
				}
				h.handleFailoverExhausted(c, rejection, streamStarted)
				requestContext = c
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			require.NotNil(t, requestContext)
			require.True(t, service.HasOpsClientBusinessLimited(requestContext))
			require.Equal(t, service.OpsClientBusinessLimitedReasonLocalPolicyDenied, service.OpsClientBusinessLimitedReason(requestContext))
			require.Contains(t, recorder.Body.String(), rejection.ClientMessage)
			if streamStarted {
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Contains(t, recorder.Body.String(), "event: response.failed")
			} else {
				require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
				require.Equal(t, "scheduling_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
			}
			streamErr, ok := service.GetOpsStreamError(requestContext)
			require.True(t, ok)
			require.True(t, streamErr.RequestScoped)
			require.Equal(t, !streamStarted, streamErr.NonStream)
			require.Equal(t, "scheduling_error", streamErr.ErrType)
			require.Equal(t, http.StatusServiceUnavailable, streamErr.IntendedStatus)
			require.Zero(t, streamErr.UpstreamStatus)
			require.Empty(t, streamErr.UpstreamErrors)
			attempts, _ := requestContext.Get(service.OpsUpstreamErrorsKey)
			if priorFailure {
				require.Equal(t, priorEvents, attempts)
				require.Equal(t, http.StatusTooManyRequests, requestContext.GetInt(service.OpsUpstreamStatusCodeKey))
			} else {
				require.Empty(t, attempts)
				require.Zero(t, requestContext.GetInt(service.OpsUpstreamStatusCodeKey))
			}
			expectedRows := int64(1)
			if priorFailure {
				expectedRows++
			}
			require.Equal(t, expectedRows, OpsErrorLogQueueLength())
			entry := (<-opsErrorLogQueue).entry
			require.Equal(t, http.StatusServiceUnavailable, entry.StatusCode)
			require.Equal(t, streamStarted, entry.Stream)
			require.True(t, entry.IsBusinessLimited)
			require.NotEqual(t, "provider", entry.ErrorOwner)
			require.NotEqual(t, "upstream_http", entry.ErrorSource)
			require.Equal(t, service.PlatformOpenAI, entry.Platform)
			require.Nil(t, entry.AccountID)
			require.Empty(t, entry.UpstreamModel)
			require.Empty(t, entry.UpstreamEndpoint)
			require.Nil(t, entry.UpstreamStatusCode)
			require.Nil(t, entry.UpstreamErrorMessage)
			require.Nil(t, entry.UpstreamErrorDetail)
			require.Empty(t, entry.UpstreamErrors)
			require.Nil(t, entry.UpstreamErrorsJSON)
			if priorFailure {
				entry = (<-opsErrorLogQueue).entry
				require.Equal(t, http.StatusOK, entry.StatusCode)
				require.Equal(t, "upstream_error", entry.ErrorType)
				require.False(t, entry.IsBusinessLimited)
				require.Equal(t, "provider", entry.ErrorOwner)
				require.Equal(t, "upstream_http", entry.ErrorSource)
				require.Equal(t, service.PlatformGrok, entry.Platform)
				require.NotNil(t, entry.AccountID)
				require.Equal(t, priorEvents[0].AccountID, *entry.AccountID)
				require.Equal(t, priorEvents[0].Model, entry.UpstreamModel)
				require.Empty(t, entry.UpstreamEndpoint)
				require.NotNil(t, entry.UpstreamStatusCode)
				require.Equal(t, http.StatusTooManyRequests, *entry.UpstreamStatusCode)
				require.NotNil(t, entry.UpstreamErrorsJSON)
				events, err := service.ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
				require.NoError(t, err)
				require.Len(t, events, 1)
				require.Equal(t, priorEvents[0].AccountID, events[0].AccountID)
				require.Equal(t, priorEvents[0].AccountName, events[0].AccountName)
				require.Equal(t, priorEvents[0].Model, events[0].Model)
				require.Equal(t, priorEvents[0].UpstreamStatusCode, events[0].UpstreamStatusCode)
				require.Equal(t, priorEvents[0].Message, events[0].Message)
			}
		})
	}
}
