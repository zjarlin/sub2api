package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type visionPolicyRepo struct {
	SettingRepository
	raw string
	err error
}

func (r *visionPolicyRepo) GetValue(_ context.Context, key string) (string, error) {
	if key != SettingKeyVisionFallbackPolicy {
		return "", ErrSettingNotFound
	}
	return r.raw, r.err
}
func (r *visionPolicyRepo) Set(_ context.Context, key, value string) error {
	if r.err != nil {
		return r.err
	}
	if key != SettingKeyVisionFallbackPolicy {
		return fmt.Errorf("unexpected setting key: %s", key)
	}
	r.raw = value
	return nil
}

func TestVisionFallbackPolicyPersistence(t *testing.T) {
	ctx := context.Background()
	cfg := visionTestConfig()
	cfg.Gateway.VisionFallback.Model = "legacy-vision"
	cfg.Gateway.VisionFallback.CandidateTimeoutSeconds = 17
	r := &visionPolicyRepo{}
	s := NewSettingService(r, cfg)
	p, err := s.GetVisionFallbackPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"legacy-vision"}, p.Models)
	require.Equal(t, 17, p.CandidateTimeoutSeconds)
	require.True(t, p.AllowUnlistedModels)
	p.Models = []string{"my-preferred", "my-backup"}
	p.AllowUnlistedModels = false
	p.Enabled = false
	require.NoError(t, s.SetVisionFallbackPolicy(ctx, p))
	loaded, err := s.GetVisionFallbackPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, p, loaded)
	for _, raw := range []string{"broken", `{"enabled":true}`, `null`} {
		r.raw = raw
		_, err = s.GetVisionFallbackPolicy(ctx)
		require.Error(t, err)
	}
	r.err = errors.New("database unavailable")
	_, err = s.GetVisionFallbackPolicy(ctx)
	require.ErrorIs(t, err, r.err)
	require.ErrorIs(t, s.SetVisionFallbackPolicy(ctx, p), r.err)
	r.err = ErrSettingNotFound
	loaded, err = s.GetVisionFallbackPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"legacy-vision"}, loaded.Models)
}

func TestVisionFallbackPolicyValidationAndCandidateOrder(t *testing.T) {
	p := DefaultVisionFallbackPolicy(visionTestConfig())
	p.Models = []string{"preferred", "backup"}
	p.AllowUnlistedModels = false
	accounts := []Account{
		visionTestAccount(1, "unlisted", "image"), visionTestAccount(2, "backup", "image"),
		visionTestAccount(3, "preferred", "image"), visionTestAccount(4, "preferred", "image"),
		visionTestAccount(5, "text-only", "text"),
	}
	accounts[3].Priority = -1
	ids := func(candidates []visionFallbackCandidate) []int64 {
		var result []int64
		for _, candidate := range candidates {
			result = append(result, candidate.account.ID)
		}
		return result
	}
	require.NoError(t, p.Validate())
	require.Equal(t, []int64{4, 3, 2}, ids(visionFallbackCandidatesWithPolicy(accounts, p, nil, nil)))
	p.AllowUnlistedModels = true
	require.Equal(t, []int64{4, 3, 2, 1}, ids(visionFallbackCandidatesWithPolicy(accounts, p, nil, nil)))
	group := &Group{ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"backup"}}}
	require.Equal(t, []int64{2}, ids(visionFallbackCandidatesWithPolicy(accounts, p, group, nil)))
	for _, models := range [][]string{{"x", "x"}, {""}, {"a b"}, {"a*"}, {strings.Repeat("a", 201)}} {
		p.Models = models
		require.Error(t, p.Validate())
	}
	p.Models = nil
	p.AllowUnlistedModels = false
	require.Error(t, p.Validate())
	p.Enabled = false
	require.NoError(t, p.Validate())
	p.TimeoutSeconds = 0
	require.Error(t, p.Validate())
	p.TimeoutSeconds = 9223372037
	require.Error(t, p.Validate())
}

func TestVisionFallbackConfiguredPolicyHotReloadAndManifest(t *testing.T) {
	ctx := context.Background()
	cfg := visionTestConfig()
	cfg.Gateway.VisionFallback.Enabled = false // 保存的策略可覆盖环境变量。
	settings := NewSettingService(&visionPolicyRepo{}, cfg)
	p := DefaultVisionFallbackPolicy(cfg)
	p.Enabled, p.AllowUnlistedModels, p.Models = true, false, []string{"backup"}
	require.NoError(t, settings.SetVisionFallbackPolicy(ctx, p))
	primary := visionTestAccount(1, "text-model", "text")
	helper := visionTestAccount(2, "backup", "image")
	accounts := []Account{primary, helper}
	svc := &OpenAIGatewayService{cfg: cfg, settingService: settings,
		accountRepo: &countingCodexModelsAccountRepo{accounts: accounts},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
			require.Equal(t, helper.ID, id)
			return visionTestResponse("backup", "configured description"), nil
		}},
	}
	body := []byte(visionTestInput)
	c, _ := visionTestContext(body, 9, 7)
	converted, err := svc.prepareVisionFallback(ctx, c, &primary, body)
	require.NoError(t, err)
	require.Contains(t, string(converted), "configured description")
	manifest := []byte(`{"models":[{"slug":"text-model","input_modalities":["text"]}]}`)
	group := &Group{ID: 7, Platform: PlatformOpenAI}
	assisted, err := applyConfiguredVisionFallbackManifest(ctx, settings, manifest, cfg, group, group.Platform, accounts, nil, true)
	require.NoError(t, err)
	require.Contains(t, string(assisted), `"image"`)
	p.Enabled = false
	require.NoError(t, settings.SetVisionFallbackPolicy(ctx, p))
	c, _ = visionTestContext(body, 9, 7)
	converted, err = svc.prepareVisionFallback(ctx, c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, body, converted)
	plain, err := applyConfiguredVisionFallbackManifest(ctx, settings, manifest, cfg, group, group.Platform, accounts, nil, true)
	require.NoError(t, err)
	require.JSONEq(t, string(manifest), string(plain))
}

func TestVisionFallbackOrderedRecoveryAcrossImagesAndPrimaryRetries(t *testing.T) {
	ctx := context.Background()
	cfg := visionTestConfig()
	settings := NewSettingService(&visionPolicyRepo{}, cfg)
	p := DefaultVisionFallbackPolicy(cfg)
	p.Models, p.AllowUnlistedModels = []string{"preferred", "backup"}, false
	require.NoError(t, settings.SetVisionFallbackPolicy(ctx, p))
	primary := visionTestAccount(1, "text-model", "text")
	accounts := []Account{visionTestAccount(2, "backup", "image"), visionTestAccount(3, "preferred", "image"), visionTestAccount(4, "preferred", "image")}
	var calls []int64
	var deadlines []time.Time
	svc := &OpenAIGatewayService{cfg: cfg, settingService: settings,
		accountRepo: &countingCodexModelsAccountRepo{accounts: accounts},
		httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
			calls = append(calls, id)
			deadline, _ := req.Context().Deadline()
			deadlines = append(deadlines, deadline)
			if id != 2 {
				return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"provider failed"}}`))}, nil
			}
			return visionTestResponse("backup", "backup observation"), nil
		}},
	}
	body := []byte(strings.Replace(visionTestInput, `"detail":"original"}`, `"detail":"original"},{"type":"input_image","image_url":"data:image/png;base64,BBBB"}`, 1))
	c, recorder := visionTestContext(body, 9, 7)
	converted, err := svc.prepareVisionFallback(ctx, c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, []int64{3, 4, 2, 2}, calls)
	require.Equal(t, 2, strings.Count(string(converted), "backup observation"))
	require.Empty(t, recorder.Body.String())
	state, err := svc.visionFallbackState(ctx, c)
	require.NoError(t, err)
	deadline := state.deadline
	usage := TakeVisionFallbackUsage(c)
	require.Len(t, usage, 2)
	require.NotEqual(t, usage[0].Result.RequestID, usage[1].Result.RequestID)
	require.Empty(t, TakeVisionFallbackUsage(c))
	_, err = svc.prepareVisionFallback(ctx, c, &primary, body)
	require.NoError(t, err)
	require.Len(t, calls, 4)
	require.Equal(t, deadline, state.deadline)
	require.Empty(t, TakeVisionFallbackUsage(c))
	value, _ := c.Get(OpsUpstreamErrorsKey)
	events := value.([]*OpsUpstreamErrorEvent)
	require.Len(t, events, 2)
	for _, event := range events {
		require.Equal(t, "vision_helper", event.Stage)
		require.Equal(t, "preferred", event.Model)
		require.Equal(t, "backup", event.RecoveredByModel)
		require.Equal(t, int64(2), event.RecoveredByAccountID)
	}
	// WS 下一回合必须重新加载策略和预算；同一回合重试仍使用旧快照。
	p.Enabled = false
	require.NoError(t, settings.SetVisionFallbackPolicy(ctx, p))
	BeginOpsStreamTurn(c, 2)
	converted, err = svc.prepareVisionFallback(ctx, c, &primary, body)
	require.NoError(t, err)
	require.Equal(t, body, converted)
	next, err := svc.visionFallbackState(ctx, c)
	require.NoError(t, err)
	require.NotSame(t, state, next)
	require.False(t, next.policy.Enabled)
}

func TestVisionFallbackExhaustionBudgetAndCancellation(t *testing.T) {
	for _, scenario := range []string{"exhausted", "budget", "cancelled", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			primary := visionTestAccount(1, "text-model", "text")
			helper := visionTestAccount(2, "vision", "image")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: &countingCodexModelsAccountRepo{accounts: []Account{helper}},
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
					calls++
					if scenario == "cancelled" {
						cancel()
						return nil, ctx.Err()
					}
					if scenario == "empty" {
						return visionTestResponse("vision", ""), nil
					}
					return nil, errors.New("private network detail")
				}},
			}
			body := []byte(visionTestInput)
			c, recorder := visionTestContext(body, 9, 7)
			state, err := svc.visionFallbackState(ctx, c)
			require.NoError(t, err)
			if scenario == "budget" {
				state.deadline = time.Now().Add(-time.Second)
			}
			for retry := 0; retry < 2; retry++ {
				converted, err := svc.prepareVisionFallback(ctx, c, &primary, body)
				require.Error(t, err)
				require.Nil(t, converted)
				require.NotContains(t, err.Error(), "private")
				if scenario == "cancelled" {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					var failure *UpstreamFailoverError
					require.ErrorAs(t, err, &failure)
					require.False(t, failure.ShouldReportAccountScheduleFailure())
					if scenario == "budget" {
						require.Equal(t, http.StatusGatewayTimeout, failure.ClientStatusCode)
					}
				}
			}
			if scenario == "budget" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
			if scenario == "empty" {
				value, _ := c.Get(OpsUpstreamErrorsKey)
				events := value.([]*OpsUpstreamErrorEvent)
				require.Len(t, events, 1)
				require.Contains(t, events[0].Message, "empty or oversized")
			}
			require.Empty(t, recorder.Body.String())
			require.False(t, c.Writer.Written())
			require.Equal(t, visionTestInput, string(body))
		})
	}
}
