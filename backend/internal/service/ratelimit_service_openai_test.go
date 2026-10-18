//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCalculateOpenAI429ResetTime_7dExhausted(t *testing.T) {
	svc := &RateLimitService{}

	// Simulate headers when 7d limit is exhausted (100% used)
	// Primary = 7d (10080 minutes), Secondary = 5h (300 minutes)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "384607") // ~4.5 days
	headers.Set("x-codex-primary-window-minutes", "10080")       // 7 days
	headers.Set("x-codex-secondary-used-percent", "3")
	headers.Set("x-codex-secondary-reset-after-seconds", "17369") // ~4.8 hours
	headers.Set("x-codex-secondary-window-minutes", "300")        // 5 hours

	before := time.Now()
	resetAt := svc.calculateOpenAI429ResetTime(headers)
	after := time.Now()

	if resetAt == nil {
		t.Fatal("expected non-nil resetAt")
	}

	// Should be approximately 384607 seconds from now
	expectedDuration := 384607 * time.Second
	minExpected := before.Add(expectedDuration)
	maxExpected := after.Add(expectedDuration)

	if resetAt.Before(minExpected) || resetAt.After(maxExpected) {
		t.Errorf("resetAt %v not in expected range [%v, %v]", resetAt, minExpected, maxExpected)
	}
}

func TestClinepassQuotaResetUsesReportedDuration(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    time.Duration
	}{
		{"Error 429: You have reached your monthly Clinepass limit. The limit resets in 21d 3h, please try again later.", 21*24*time.Hour + 3*time.Hour},
		{"Error 429: You have reached your weekly Clinepass limit. The limit resets in 2d 2h, please try again later.", 2*24*time.Hour + 2*time.Hour},
		{"You have reached your monthly Clinepass limit. The limit resets in 18d 3h, please try again later.", 18*24*time.Hour + 3*time.Hour},
		{"Rate limit reached, retry in 21d 3h", 0},
		{"You have reached your monthly Clinepass limit. The limit resets in 999999999999999999999d, please try again later.", 0},
		{"You have reached your monthly Clinepass limit. The limit resets in 33d, please try again later.", 0},
	} {
		t.Run(tc.message, func(t *testing.T) {
			before := time.Now()
			reset := parseOpenAIRateLimitResetTime([]byte(fmt.Sprintf(`{"error":{"message":%q,"type":"upstream_error"}}`, tc.message)))
			if tc.want == 0 {
				require.Nil(t, reset)
				return
			}
			require.NotNil(t, reset)
			require.WithinDuration(t, before.Add(tc.want), time.Unix(*reset, 0), 2*time.Second)
		})
	}
}

func TestCalculateOpenAI429ResetTime_5hExhausted(t *testing.T) {
	svc := &RateLimitService{}

	// Simulate headers when 5h limit is exhausted (100% used)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "50")
	headers.Set("x-codex-primary-reset-after-seconds", "500000")
	headers.Set("x-codex-primary-window-minutes", "10080") // 7 days
	headers.Set("x-codex-secondary-used-percent", "100")
	headers.Set("x-codex-secondary-reset-after-seconds", "3600") // 1 hour
	headers.Set("x-codex-secondary-window-minutes", "300")       // 5 hours

	before := time.Now()
	resetAt := svc.calculateOpenAI429ResetTime(headers)
	after := time.Now()

	if resetAt == nil {
		t.Fatal("expected non-nil resetAt")
	}

	// Should be approximately 3600 seconds from now
	expectedDuration := 3600 * time.Second
	minExpected := before.Add(expectedDuration)
	maxExpected := after.Add(expectedDuration)

	if resetAt.Before(minExpected) || resetAt.After(maxExpected) {
		t.Errorf("resetAt %v not in expected range [%v, %v]", resetAt, minExpected, maxExpected)
	}
}

func TestCalculateOpenAI429ResetTime_NeitherExhausted_ReturnsNil(t *testing.T) {
	svc := &RateLimitService{}

	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "80")
	headers.Set("x-codex-primary-reset-after-seconds", "100000")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "90")
	headers.Set("x-codex-secondary-reset-after-seconds", "5000")
	headers.Set("x-codex-secondary-window-minutes", "300")

	require.Nil(t, svc.calculateOpenAI429ResetTime(headers))
}

func TestCalculateOpenAI429ResetTime_NoCodexHeaders(t *testing.T) {
	svc := &RateLimitService{}

	// No codex headers at all
	headers := http.Header{}
	headers.Set("content-type", "application/json")

	resetAt := svc.calculateOpenAI429ResetTime(headers)

	if resetAt != nil {
		t.Errorf("expected nil resetAt when no codex headers, got %v", resetAt)
	}
}

func TestParseOpenAIRateLimitResetTime_OpenCodeGoUsageLimit(t *testing.T) {
	tests := []struct {
		name string
		body string
		want time.Duration
	}{
		{
			name: "days",
			body: `{"type":"error","error":{"type":"GoUsageLimitError","message":"Weekly usage limit reached. Resets in 2 days."}}`,
			want: 48 * time.Hour,
		},
		{
			name: "hours",
			body: `{"type":"error","error":{"type":"GoUsageLimitError","message":"Weekly usage limit reached. Resets in 18 hours."}}`,
			want: 18 * time.Hour,
		},
		{
			name: "hours and minutes",
			body: `{"type":"error","error":{"type":"GoUsageLimitError","message":"5-hour usage limit reached. Resets in 4hr 59min."}}`,
			want: 4*time.Hour + 59*time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			resetAt := parseOpenAIRateLimitResetTime([]byte(tt.body))
			after := time.Now()

			require.NotNil(t, resetAt)
			actual := time.Unix(*resetAt, 0)
			require.False(t, actual.Before(before.Add(tt.want).Truncate(time.Second)))
			require.False(t, actual.After(after.Add(tt.want)))
		})
	}
}

func TestParseOpenAIRateLimitResetTime_DoesNotParseUnknownErrorMessage(t *testing.T) {
	body := []byte(`{"error":{"type":"rate_limit_error","message":"Resets in 2 days."}}`)

	require.Nil(t, parseOpenAIRateLimitResetTime(body))
}

// TestParseOpenAIRateLimitResetTime_BuiltinAdapterPoolReset 内置适配器（traework）
// 整池冷却时返回 {"error":{"type":"rate_limit_error","code":4008,"resets_at":<unix>}}；
// 通用规则不读 rate_limit_error 的 resets_at，必须单独识别，否则账号被秒级兜底反复重试。
func TestParseOpenAIRateLimitResetTime_BuiltinAdapterPoolReset(t *testing.T) {
	resetAt := time.Now().Add(6 * time.Hour).Truncate(time.Second)
	body := []byte(fmt.Sprintf(`{"error":{"message":"all traework accounts exhausted (cooling); retry after pool reset","type":"rate_limit_error","code":4008,"resets_at":%d}}`, resetAt.Unix()))
	parsed := parseOpenAIRateLimitResetTime(body)
	require.NotNil(t, parsed)
	require.Equal(t, resetAt.Unix(), *parsed)

	// 字符串形式同样接受。
	bodyString := []byte(fmt.Sprintf(`{"error":{"type":"rate_limit_error","code":4008,"resets_at":"%d"}}`, resetAt.Unix()))
	parsed = parseOpenAIRateLimitResetTime(bodyString)
	require.NotNil(t, parsed)
	require.Equal(t, resetAt.Unix(), *parsed)
}

// 其它平台/业务码的 rate_limit_error 不应被这条规则误吞。
func TestParseOpenAIRateLimitResetTime_BuiltinAdapterScopeLimited(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"rate_limit_error","code":4009,"resets_at":4102444800}}`,
		`{"error":{"type":"rate_limit_error","resets_at":4102444800}}`,
		`{"error":{"type":"rate_limit_error","code":4008}}`,
		`{"error":{"type":"rate_limit_error","code":4008,"resets_at":0}}`,
		`{"error":{"type":"rate_limit_error","code":4008,"resets_at":"soon"}}`,
		`{"error":{"type":"upstream_error","code":4008,"resets_at":4102444800}}`,
	} {
		require.Nil(t, parseOpenAIRateLimitResetTime([]byte(body)), body)
	}
}

func TestParseOpenAIRateLimitResetTime_CommandCodeWeeklyLimit(t *testing.T) {
	reset := time.Now().Add(5 * 24 * time.Hour).Truncate(time.Millisecond)
	body := []byte(fmt.Sprintf(`{"error":{"code":"RATE_LIMITED","type":"rate_limit_error","message":"You've reached your weekly usage limit for your plan. Your limit resets at %s. Please wait for the window to reset or upgrade your plan to continue."}}`, reset.UTC().Format(time.RFC3339Nano)))
	parsed := parseOpenAIRateLimitResetTime(body)
	require.NotNil(t, parsed)
	require.Equal(t, reset.Unix(), *parsed)
	for _, value := range []string{"invalid", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "2099-01-01T00:00:00Z-untrusted"} {
		body := []byte(fmt.Sprintf(`{"error":{"code":"RATE_LIMITED","type":"rate_limit_error","message":"weekly usage limit for your plan. Your limit resets at %s. Please wait."}}`, value))
		require.Nil(t, parseOpenAIRateLimitResetTime(body))
	}
}

func TestCalculateOpenAI429ResetTime_ReversedWindowOrder(t *testing.T) {
	svc := &RateLimitService{}

	// Test when OpenAI sends primary as 5h and secondary as 7d (reversed)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")         // This is 5h
	headers.Set("x-codex-primary-reset-after-seconds", "3600") // 1 hour
	headers.Set("x-codex-primary-window-minutes", "300")       // 5 hours - smaller!
	headers.Set("x-codex-secondary-used-percent", "50")
	headers.Set("x-codex-secondary-reset-after-seconds", "500000")
	headers.Set("x-codex-secondary-window-minutes", "10080") // 7 days - larger!

	before := time.Now()
	resetAt := svc.calculateOpenAI429ResetTime(headers)
	after := time.Now()

	if resetAt == nil {
		t.Fatal("expected non-nil resetAt")
	}

	// Should correctly identify that primary is 5h (smaller window) and use its reset time
	expectedDuration := 3600 * time.Second
	minExpected := before.Add(expectedDuration)
	maxExpected := after.Add(expectedDuration)

	if resetAt.Before(minExpected) || resetAt.After(maxExpected) {
		t.Errorf("resetAt %v not in expected range [%v, %v]", resetAt, minExpected, maxExpected)
	}
}

type openAI429SnapshotRepo struct {
	mockAccountRepoForGemini
	rateLimitedID      int64
	updatedExtra       map[string]any
	bulkUpdatedIDs     []int64
	bulkUpdatedPayload AccountBulkUpdate
}

func (r *openAI429SnapshotRepo) SetRateLimited(_ context.Context, id int64, _ time.Time) error {
	r.rateLimitedID = id
	return nil
}

func (r *openAI429SnapshotRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updatedExtra = updates
	return nil
}

func (r *openAI429SnapshotRepo) BulkUpdate(_ context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	r.bulkUpdatedIDs = append([]int64(nil), ids...)
	r.bulkUpdatedPayload = updates
	return int64(len(ids)), nil
}

func TestHandle429_OpenAIPersistsCodexSnapshotImmediately(t *testing.T) {
	repo := &openAI429SnapshotRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := &Account{ID: 123, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "100")
	headers.Set("x-codex-secondary-reset-after-seconds", "18000")
	headers.Set("x-codex-secondary-window-minutes", "300")

	svc.handle429(context.Background(), account, headers, nil)

	if repo.rateLimitedID != account.ID {
		t.Fatalf("rateLimitedID = %d, want %d", repo.rateLimitedID, account.ID)
	}
	if len(repo.updatedExtra) == 0 {
		t.Fatal("expected codex snapshot to be persisted on 429")
	}
	if got := repo.updatedExtra["codex_5h_used_percent"]; got != 100.0 {
		t.Fatalf("codex_5h_used_percent = %v, want 100", got)
	}
	if got := repo.updatedExtra["codex_7d_used_percent"]; got != 100.0 {
		t.Fatalf("codex_7d_used_percent = %v, want 100", got)
	}
}

func TestHandle429_OpenAISyncsObservedPlanType(t *testing.T) {
	repo := &openAI429SnapshotRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := &Account{
		ID:          124,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"plan_type": "plus"},
	}
	body := []byte(`{"error":{"type":"usage_limit_reached","message":"limit reached","plan_type":"free","resets_at":1777283883}}`)

	svc.handle429(context.Background(), account, http.Header{}, body)

	require.Equal(t, []int64{account.ID}, repo.bulkUpdatedIDs)
	require.Equal(t, "free", repo.bulkUpdatedPayload.Credentials["plan_type"])
	require.Equal(t, "free", account.Credentials["plan_type"])
	require.Equal(t, account.ID, repo.rateLimitedID)
}

// TestHandle429_SkipsSparkShadow 外审第8轮 P1:spark 影子的限流状态只由 QueryUsage(/wham/usage
// codex_bengalfox)维护;/responses 429 携带的 global x-codex-* 不得对影子做任何 DB 限流写入,
// 否则会把 spark 误耦合到 global codex 窗口、冷却到 global reset。
func TestHandle429_SkipsSparkShadow(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "100")
	headers.Set("x-codex-secondary-reset-after-seconds", "18000")
	headers.Set("x-codex-secondary-window-minutes", "300")

	parentID := int64(900)
	shadowRepo := &openAI429SnapshotRepo{}
	shadowSvc := NewRateLimitService(shadowRepo, nil, nil, nil, nil)
	shadow := &Account{
		ID:              901,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
	}

	shadowSvc.handle429(context.Background(), shadow, headers, nil)

	require.Zero(t, shadowRepo.rateLimitedID, "spark shadow must not be SetRateLimited from /responses global 429")
	require.Empty(t, shadowRepo.updatedExtra, "spark shadow must not get a codex snapshot from /responses 429")

	// 反向对照:普通 OpenAI OAuth 账号仍按 global 429 限流。
	normalRepo := &openAI429SnapshotRepo{}
	normalSvc := NewRateLimitService(normalRepo, nil, nil, nil, nil)
	normal := &Account{ID: 902, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	normalSvc.handle429(context.Background(), normal, headers, nil)

	require.Equal(t, normal.ID, normalRepo.rateLimitedID, "normal OpenAI OAuth account should still be rate limited")
}

func TestNormalizedCodexLimits(t *testing.T) {
	// Test the Normalize() method directly
	pUsed := 100.0
	pReset := 384607
	pWindow := 10080
	sUsed := 3.0
	sReset := 17369
	sWindow := 300

	snapshot := &OpenAICodexUsageSnapshot{
		PrimaryUsedPercent:         &pUsed,
		PrimaryResetAfterSeconds:   &pReset,
		PrimaryWindowMinutes:       &pWindow,
		SecondaryUsedPercent:       &sUsed,
		SecondaryResetAfterSeconds: &sReset,
		SecondaryWindowMinutes:     &sWindow,
	}

	normalized := snapshot.Normalize()
	if normalized == nil {
		t.Fatal("expected non-nil normalized")
	}

	// Primary has larger window (10080 > 300), so primary should be 7d
	if normalized.Used7dPercent == nil || *normalized.Used7dPercent != 100.0 {
		t.Errorf("expected Used7dPercent=100, got %v", normalized.Used7dPercent)
	}
	if normalized.Reset7dSeconds == nil || *normalized.Reset7dSeconds != 384607 {
		t.Errorf("expected Reset7dSeconds=384607, got %v", normalized.Reset7dSeconds)
	}
	if normalized.Used5hPercent == nil || *normalized.Used5hPercent != 3.0 {
		t.Errorf("expected Used5hPercent=3, got %v", normalized.Used5hPercent)
	}
	if normalized.Reset5hSeconds == nil || *normalized.Reset5hSeconds != 17369 {
		t.Errorf("expected Reset5hSeconds=17369, got %v", normalized.Reset5hSeconds)
	}
}

func TestNormalizedCodexLimits_OnlyPrimaryData(t *testing.T) {
	// Test when only primary has data, no window_minutes
	pUsed := 80.0
	pReset := 50000

	snapshot := &OpenAICodexUsageSnapshot{
		PrimaryUsedPercent:       &pUsed,
		PrimaryResetAfterSeconds: &pReset,
		// No window_minutes, no secondary data
	}

	normalized := snapshot.Normalize()
	if normalized == nil {
		t.Fatal("expected non-nil normalized")
	}

	// Legacy assumption: primary=7d, secondary=5h
	if normalized.Used7dPercent == nil || *normalized.Used7dPercent != 80.0 {
		t.Errorf("expected Used7dPercent=80, got %v", normalized.Used7dPercent)
	}
	if normalized.Reset7dSeconds == nil || *normalized.Reset7dSeconds != 50000 {
		t.Errorf("expected Reset7dSeconds=50000, got %v", normalized.Reset7dSeconds)
	}
	// Secondary (5h) should be nil
	if normalized.Used5hPercent != nil {
		t.Errorf("expected Used5hPercent=nil, got %v", *normalized.Used5hPercent)
	}
	if normalized.Reset5hSeconds != nil {
		t.Errorf("expected Reset5hSeconds=nil, got %v", *normalized.Reset5hSeconds)
	}
}

func TestRateLimitService_HandleUpstreamError_403PreservesOriginalUpstreamMessage(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       201,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
	}

	shouldDisable := service.HandleUpstreamError(
		context.Background(),
		account,
		403,
		http.Header{},
		[]byte(`{"error":{"message":"workspace forbidden by policy","type":"invalid_request_error"}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Contains(t, repo.lastErrorMsg, "workspace forbidden by policy")
	require.NotContains(t, repo.lastErrorMsg, "account may be suspended or lack permissions")
}

func TestRateLimitService_HandleUpstreamError_403FallsBackToRawBody(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       202,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
	}

	shouldDisable := service.HandleUpstreamError(
		context.Background(),
		account,
		403,
		http.Header{},
		[]byte(`{"error":{"type":"access_denied","details":{"reason":"ip_blocked"}}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Contains(t, repo.lastErrorMsg, `"access_denied"`)
	require.Contains(t, repo.lastErrorMsg, `"ip_blocked"`)
	require.NotContains(t, repo.lastErrorMsg, "account may be suspended or lack permissions")
}

func TestNormalizedCodexLimits_OnlySecondaryData(t *testing.T) {
	// Test when only secondary has data, no window_minutes
	sUsed := 60.0
	sReset := 3000

	snapshot := &OpenAICodexUsageSnapshot{
		SecondaryUsedPercent:       &sUsed,
		SecondaryResetAfterSeconds: &sReset,
		// No window_minutes, no primary data
	}

	normalized := snapshot.Normalize()
	if normalized == nil {
		t.Fatal("expected non-nil normalized")
	}

	// Legacy assumption: primary=7d, secondary=5h
	// So secondary goes to 5h
	if normalized.Used5hPercent == nil || *normalized.Used5hPercent != 60.0 {
		t.Errorf("expected Used5hPercent=60, got %v", normalized.Used5hPercent)
	}
	if normalized.Reset5hSeconds == nil || *normalized.Reset5hSeconds != 3000 {
		t.Errorf("expected Reset5hSeconds=3000, got %v", normalized.Reset5hSeconds)
	}
	// Primary (7d) should be nil
	if normalized.Used7dPercent != nil {
		t.Errorf("expected Used7dPercent=nil, got %v", *normalized.Used7dPercent)
	}
}

func TestNormalizedCodexLimits_BothDataNoWindowMinutes(t *testing.T) {
	// Test when both have data but no window_minutes
	pUsed := 100.0
	pReset := 400000
	sUsed := 50.0
	sReset := 10000

	snapshot := &OpenAICodexUsageSnapshot{
		PrimaryUsedPercent:         &pUsed,
		PrimaryResetAfterSeconds:   &pReset,
		SecondaryUsedPercent:       &sUsed,
		SecondaryResetAfterSeconds: &sReset,
		// No window_minutes
	}

	normalized := snapshot.Normalize()
	if normalized == nil {
		t.Fatal("expected non-nil normalized")
	}

	// Legacy assumption: primary=7d, secondary=5h
	if normalized.Used7dPercent == nil || *normalized.Used7dPercent != 100.0 {
		t.Errorf("expected Used7dPercent=100, got %v", normalized.Used7dPercent)
	}
	if normalized.Reset7dSeconds == nil || *normalized.Reset7dSeconds != 400000 {
		t.Errorf("expected Reset7dSeconds=400000, got %v", normalized.Reset7dSeconds)
	}
	if normalized.Used5hPercent == nil || *normalized.Used5hPercent != 50.0 {
		t.Errorf("expected Used5hPercent=50, got %v", normalized.Used5hPercent)
	}
	if normalized.Reset5hSeconds == nil || *normalized.Reset5hSeconds != 10000 {
		t.Errorf("expected Reset5hSeconds=10000, got %v", normalized.Reset5hSeconds)
	}
}

func TestHandle429_AnthropicPlatformUnaffected(t *testing.T) {
	// Verify that Anthropic platform accounts still use the original logic
	// This test ensures we don't break existing Claude account rate limiting

	svc := &RateLimitService{}

	// Simulate Anthropic 429 headers
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-reset", "1737820800") // A future Unix timestamp

	// For Anthropic platform, calculateOpenAI429ResetTime should return nil
	// because it only handles OpenAI platform
	resetAt := svc.calculateOpenAI429ResetTime(headers)

	// Should return nil since there are no x-codex-* headers
	if resetAt != nil {
		t.Errorf("expected nil for Anthropic headers, got %v", resetAt)
	}
}

func TestCalculateOpenAI429ResetTime_UserProvidedScenario(t *testing.T) {
	// This is the exact scenario from the user:
	// codex_7d_used_percent: 100
	// codex_7d_reset_after_seconds: 384607 (约4.5天后重置)
	// codex_5h_used_percent: 3
	// codex_5h_reset_after_seconds: 17369 (约4.8小时后重置)

	svc := &RateLimitService{}

	// Simulate headers matching user's data
	// Note: We need to map the canonical 5h/7d back to primary/secondary
	// Based on typical OpenAI behavior: primary=7d (larger window), secondary=5h (smaller window)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "384607")
	headers.Set("x-codex-primary-window-minutes", "10080") // 7 days = 10080 minutes
	headers.Set("x-codex-secondary-used-percent", "3")
	headers.Set("x-codex-secondary-reset-after-seconds", "17369")
	headers.Set("x-codex-secondary-window-minutes", "300") // 5 hours = 300 minutes

	before := time.Now()
	resetAt := svc.calculateOpenAI429ResetTime(headers)
	after := time.Now()

	if resetAt == nil {
		t.Fatal("expected non-nil resetAt for user scenario")
	}

	// Should use the 7d reset time (384607 seconds) since 7d limit is exhausted (100%)
	expectedDuration := 384607 * time.Second
	minExpected := before.Add(expectedDuration)
	maxExpected := after.Add(expectedDuration)

	if resetAt.Before(minExpected) || resetAt.After(maxExpected) {
		t.Errorf("resetAt %v not in expected range [%v, %v]", resetAt, minExpected, maxExpected)
	}

	// Verify it's approximately 4.45 days (384607 seconds)
	duration := resetAt.Sub(before)
	actualDays := duration.Hours() / 24.0

	// 384607 / 86400 = ~4.45 days
	if actualDays < 4.4 || actualDays > 4.5 {
		t.Errorf("expected ~4.45 days, got %.2f days", actualDays)
	}

	t.Logf("User scenario: reset_at=%v, duration=%.2f days", resetAt, actualDays)
}

func TestCalculateOpenAI429ResetTime_5MinFallbackWhenNoReset(t *testing.T) {
	// Test that we return nil when there's used_percent but no reset_after_seconds
	// This should cause the caller to use the default 5-minute fallback

	svc := &RateLimitService{}

	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	// No reset_after_seconds!

	resetAt := svc.calculateOpenAI429ResetTime(headers)

	// Should return nil since there's no reset time available
	if resetAt != nil {
		t.Errorf("expected nil when no reset_after_seconds, got %v", resetAt)
	}
}

func TestParseAliyunTokenPlanQuotaReset(t *testing.T) {
	const responsesBody = `{"code":"Throttling.AllocationQuota","message":"Your token-plan 1-month quota has been exhausted. The quota will reset at 11-08 16:00:00 UTC.","request_id":"req-1"}`
	const chatBody = `{"error":{"message":"Your token-plan 1-month quota has been exhausted. The quota will reset at 11-08 16:00:00 UTC.","type":"insufficient_quota","code":"insufficient_quota"}}`

	now := time.Date(2026, 10, 14, 15, 30, 0, 0, time.UTC)
	want := time.Date(2026, 11, 8, 16, 0, 0, 0, time.UTC)
	for name, body := range map[string][]byte{
		"responses_shape": []byte(responsesBody),
		"chat_shape":      []byte(chatBody),
	} {
		t.Run(name, func(t *testing.T) {
			reset := parseAliyunTokenPlanQuotaReset(body, now)
			require.NotNil(t, reset)
			require.True(t, want.Equal(*reset))
			parsed := parseOpenAIRateLimitResetTime(body)
			require.NotNil(t, parsed)
			require.Equal(t, want.Unix(), *parsed)
		})
	}

	t.Run("year_rollover", func(t *testing.T) {
		body := []byte(`{"code":"Throttling.AllocationQuota","message":"Your token-plan 1-month quota has been exhausted. The quota will reset at 01-01 00:00:00 UTC."}`)
		december := time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC)
		reset := parseAliyunTokenPlanQuotaReset(body, december)
		require.NotNil(t, reset)
		require.True(t, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Equal(*reset))
	})

	t.Run("invalid_and_unrelated", func(t *testing.T) {
		for _, body := range []string{
			`{"code":"Throttling.AllocationQuota","message":"quota exhausted"}`,
			`{"code":"Throttling.AllocationQuota","message":"The quota will reset at 02-30 16:00:00 UTC."}`,
			`{"code":"Throttling.AllocationQuota","message":"Your token-plan 1-month quota has been exhausted. The quota will reset at 11-08 16:99:00 UTC."}`,
			`{"code":"Throttling.AllocationQuota","message":"Your token-plan 1-month quota has been exhausted. The quota will reset at 11-08 25:00:00 UTC."}`,
			`{"error":{"code":"insufficient_quota","message":"Your token-plan 1-month quota has been exhausted. The quota will reset at 11-08 16:00:00 CST."}}`,
			`{"code":"rate_limit_error","message":"Your token-plan 1-month quota has been exhausted. The quota will reset at 11-08 16:00:00 UTC."}`,
			`{"error":{"message":"User asked: your token-plan 1-month quota has been exhausted. The quota will reset at 11-08 16:00:00 UTC."}}`,
		} {
			require.Nil(t, parseAliyunTokenPlanQuotaReset([]byte(body), now), body)
			require.Nil(t, parseOpenAIRateLimitResetTime([]byte(body)), body)
		}
	})
}

func TestAliyunTokenPlanQuotaUsesTimedRateLimitNotAccessState(t *testing.T) {
	const body = `{"code":"Throttling.AllocationQuota","message":"Your token-plan 1-month quota has been exhausted. The quota will reset at 11-08 16:00:00 UTC.","request_id":"req-1"}`
	require.False(t, isOpenAIUpstreamAccessStateError("", []byte(body)))
	require.False(t, isOpenAIUpstreamAccessStateCodeWithReset([]byte(`{"code":"insufficient_quota","message":"quota exhausted"}`)))
	require.True(t, isOpenAIUpstreamAccessStateError("", []byte(`{"error":{"code":"insufficient_quota","message":"quota exhausted"}}`)))

	repo := &aliyunTokenPlanQuotaRepoStub{}
	rateLimits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc := &OpenAIGatewayService{rateLimitService: rateLimits}
	account := &Account{ID: 883, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	headers := http.Header{"Retry-After": {"120"}}
	require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, headers, []byte(body), "deepseek-v4-flash-0731"))
	require.Zero(t, repo.setErrorCalls, "quota reset must not permanently disable the account")
	require.Zero(t, repo.tempCalls)
	require.Equal(t, 1, repo.rateLimitCalls)
	require.Equal(t, time.Date(2026, 11, 8, 16, 0, 0, 0, time.UTC), repo.lastRateLimitReset.UTC())
}

// aliyunTokenPlanQuotaRepoStub 记录 429 冷却与永久禁用两条路径，用于区分
// 带未来重置点的配额错误究竟走定时冷却还是账号级 SetError。
type aliyunTokenPlanQuotaRepoStub struct {
	AccountRepository
	tempCalls          int
	setErrorCalls      int
	rateLimitCalls     int
	lastRateLimitReset time.Time
}

func (r *aliyunTokenPlanQuotaRepoStub) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

func (r *aliyunTokenPlanQuotaRepoStub) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

func (r *aliyunTokenPlanQuotaRepoStub) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	r.rateLimitCalls++
	r.lastRateLimitReset = resetAt
	return nil
}

func TestHandle429AliyunTokenPlanBodyReset(t *testing.T) {
	repo := &rateLimit429AccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 883, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	body := []byte(`{"code":"Throttling.AllocationQuota","message":"Your token-plan 1-month quota has been exhausted. The quota will reset at 11-08 16:00:00 UTC.","request_id":"req-1"}`)
	svc.handle429(context.Background(), account, nil, body)
	require.Equal(t, 1, repo.rateLimitCalls)
	reset := repo.lastRateLimitReset.UTC()
	require.Equal(t, time.Date(2026, 11, 8, 16, 0, 0, 0, time.UTC), reset)
}
