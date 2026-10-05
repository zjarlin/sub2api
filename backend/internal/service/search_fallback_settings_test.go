package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type searchPolicyRepo struct {
	SettingRepository
	raw string
	err error
}

func (r *searchPolicyRepo) GetValue(_ context.Context, key string) (string, error) {
	if key != SettingKeySearchFallbackPolicy {
		return "", ErrSettingNotFound
	}
	return r.raw, r.err
}
func (r *searchPolicyRepo) Set(_ context.Context, key, value string) error {
	if r.err != nil {
		return r.err
	}
	r.raw = value
	return nil
}

func TestSearchFallbackPolicyUsesExactVerifiedRoute(t *testing.T) {
	ctx := context.Background()
	helper := searchTestHelper()
	body := []byte(`{"tools":[{"type":"web_search"}]}`)
	policy := DefaultSearchFallbackPolicy()
	policy.RequireVerified = true
	require.Empty(t, configuredSearchFallbackCandidates(ctx, []Account{helper}, nil, body, policy))
	target := ResolveOpenAIAccountUpstreamModelForRequest(&helper, helper.Name, false)
	evidence := SearchProbeResult{AccountID: helper.ID, Model: helper.Name, UpstreamModel: target, RouteFingerprint: searchProbeRouteFingerprint(&helper, target), Status: "supported", CheckedAt: time.Now(), SourceURLs: []string{"https://go.dev/doc/devel/release"}}
	policy.ProbeResults = []SearchProbeResult{evidence}
	require.Len(t, configuredSearchFallbackCandidates(ctx, []Account{helper}, nil, body, policy), 1)
	policy.ProbeResults[0].AccountID++
	require.Empty(t, configuredSearchFallbackCandidates(ctx, []Account{helper}, nil, body, policy))
	policy.ProbeResults[0] = evidence
	helper.Credentials["base_url"] = "https://different-channel.example"
	require.Empty(t, configuredSearchFallbackCandidates(ctx, []Account{helper}, nil, body, policy))
	helper.Credentials["base_url"] = "https://original.example"
	policy.ProbeResults[0].RouteFingerprint = searchProbeRouteFingerprint(&helper, target)
	policy.ProbeResults[0].Status = "unverified"
	require.Empty(t, configuredSearchFallbackCandidates(ctx, []Account{helper}, nil, body, policy))
	policy.RequireVerified = false
	policy.Enabled = false
	require.Empty(t, configuredSearchFallbackCandidates(ctx, []Account{helper}, nil, body, policy))
}

func TestSearchFallbackPolicyPreservesProbeEvidenceOnConfigurationSave(t *testing.T) {
	repo := &searchPolicyRepo{}
	settings := NewSettingService(repo, nil)
	policy, err := settings.GetSearchFallbackPolicy(context.Background())
	require.NoError(t, err)
	require.NoError(t, settings.SetSearchFallbackPolicy(context.Background(), policy))
	evidence := SearchProbeResult{AccountID: 2, Model: "search-model", UpstreamModel: "search-model", RouteFingerprint: "fingerprint", Status: "supported", CheckedAt: time.Now().UTC(), SourceURLs: []string{"https://go.dev"}}
	require.NoError(t, settings.SaveSearchProbeResult(context.Background(), evidence))
	policy.Models = []string{"search-model"}
	policy.RequireVerified = true
	require.NoError(t, settings.SetSearchFallbackPolicy(context.Background(), policy))
	loaded, err := settings.GetSearchFallbackPolicy(context.Background())
	require.NoError(t, err)
	require.Equal(t, []SearchProbeResult{evidence}, loaded.ProbeResults)
	evidence.Status = "unverified"
	require.NoError(t, settings.SaveSearchProbeResult(context.Background(), evidence))
	loaded, err = settings.GetSearchFallbackPolicy(context.Background())
	require.NoError(t, err)
	require.Len(t, loaded.ProbeResults, 1)
	require.Equal(t, "unverified", loaded.ProbeResults[0].Status)
	repo.err = errors.New("database unavailable")
	_, err = settings.GetSearchFallbackPolicy(context.Background())
	require.Error(t, err)
}

type searchProbeAccountRepo struct {
	codexModelsVisibilityAccountRepo
	account *Account
}

func (r searchProbeAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if id != r.account.ID {
		return nil, errors.New("unknown account")
	}
	return r.account, nil
}

func TestSearchCapabilityProbePersistsOnlyActualNativeSearchEvidence(t *testing.T) {
	for _, verified := range []bool{true, false} {
		t.Run(map[bool]string{true: "search", false: "plain-answer"}[verified], func(t *testing.T) {
			helper := searchTestHelper()
			repo := &searchPolicyRepo{}
			settings := NewSettingService(repo, nil)
			calls := 0
			svc := &OpenAIGatewayService{cfg: visionTestConfig(), settingService: settings,
				accountRepo: searchProbeAccountRepo{codexModelsVisibilityAccountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {helper}}}, account: &helper},
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
					calls++
					require.Equal(t, helper.ID, id)
					require.Equal(t, "/v1/responses", req.URL.Path)
					payload := verifiedSearchResponse
					if !verified {
						payload = strings.ReplaceAll(payload, `"type":"web_search_call"`, `"type":"reasoning"`)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
				}},
			}
			c, recorder := visionTestContext([]byte(`{}`), 9, 7)
			outcome, err := svc.ProbeSearchCapability(context.Background(), c, 7, helper.ID, helper.Name)
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Empty(t, recorder.Body.String())
			if verified {
				require.Equal(t, "supported", outcome.Status)
				require.Equal(t, []string{"https://example.com/docs"}, outcome.SourceURLs)
			} else {
				require.Equal(t, "unverified", outcome.Status)
			}
			saved, err := settings.GetSearchFallbackPolicy(context.Background())
			require.NoError(t, err)
			require.Len(t, saved.ProbeResults, 1)
			require.Equal(t, outcome.Status, saved.ProbeResults[0].Status)
			_, err = svc.ProbeSearchCapability(context.Background(), c, 8, helper.ID, helper.Name)
			require.Error(t, err)
			require.Equal(t, 1, calls, "分组外账号不得实际调用")
		})
	}
}
