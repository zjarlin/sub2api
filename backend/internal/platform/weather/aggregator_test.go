package weather

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubProvider struct {
	name         string
	capabilities Capabilities
	response     *WeatherResponse
	err          error
	calls        int
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Capabilities() Capabilities { return s.capabilities }

func (s *stubProvider) Fetch(context.Context, *WeatherRequest) (*WeatherResponse, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.response, nil
}

func testAggregator(providers ...Provider) *Aggregator {
	return &Aggregator{providers: providers, tracker: newHealthTracker(), enabled: true}
}

func TestAggregatorFallsBackToNextProvider(t *testing.T) {
	failing := &stubProvider{name: "first", capabilities: Capabilities{Current: true}, err: errors.New("upstream HTTP 500")}
	working := &stubProvider{name: "second", capabilities: Capabilities{Current: true}, response: &WeatherResponse{Current: &CurrentWeather{TemperatureC: 18}}}
	aggregator := testAggregator(failing, working)
	response, err := aggregator.Fetch(context.Background(), &WeatherRequest{Location: "Luoyang"})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if response.Provider != "second" {
		t.Fatalf("provider = %s, want second", response.Provider)
	}
	if failing.calls != 1 || working.calls != 1 {
		t.Fatalf("calls = %d/%d, want 1/1", failing.calls, working.calls)
	}
	if len(response.Attempts) != 2 || response.Attempts[0].Status != "failed" || response.Attempts[1].Status != "success" {
		t.Fatalf("attempts = %+v", response.Attempts)
	}
}

func TestAggregatorSkipsUnsupportedLocationWithoutPenalty(t *testing.T) {
	codeOnly := &stubProvider{name: "code-only", capabilities: Capabilities{Current: true}, err: ErrLocationUnsupported}
	working := &stubProvider{name: "by-name", capabilities: Capabilities{Current: true}, response: &WeatherResponse{Current: &CurrentWeather{TemperatureC: 20}}}
	aggregator := testAggregator(codeOnly, working)
	response, err := aggregator.Fetch(context.Background(), &WeatherRequest{Location: "洛阳"})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if response.Provider != "by-name" {
		t.Fatalf("provider = %s", response.Provider)
	}
	if score := aggregator.tracker.get("code-only").score(); score != 100 {
		t.Fatalf("unsupported location must not lower health score, got %.1f", score)
	}
}

func TestAggregatorFiltersHistoryCapability(t *testing.T) {
	currentOnly := &stubProvider{name: "current-only", capabilities: Capabilities{Current: true}, response: &WeatherResponse{Current: &CurrentWeather{}}}
	history := &stubProvider{name: "history", capabilities: Capabilities{History: true}, response: &WeatherResponse{History: []DailyWeather{{Date: "2026-01-01"}}}}
	aggregator := testAggregator(currentOnly, history)
	response, err := aggregator.Fetch(context.Background(), &WeatherRequest{
		Location: "57073", History: true, Start: "2026-01-01", End: "2026-01-31",
	})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if response.Provider != "history" {
		t.Fatalf("provider = %s, want history", response.Provider)
	}
	if currentOnly.calls != 0 {
		t.Fatalf("current-only provider must be filtered out for history requests, calls=%d", currentOnly.calls)
	}
}

func TestAggregatorCoolsDownAfterRepeatedFailures(t *testing.T) {
	failing := &stubProvider{name: "flaky", capabilities: Capabilities{Current: true}, err: errors.New("upstream timeout")}
	aggregator := testAggregator(failing)
	for attempt := 0; attempt < healthCooldownAfterFailures; attempt++ {
		if _, err := aggregator.Fetch(context.Background(), &WeatherRequest{Location: "Luoyang"}); err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt)
		}
	}
	before := failing.calls
	_, err := aggregator.Fetch(context.Background(), &WeatherRequest{Location: "Luoyang"})
	var chain *ChainError
	if !errors.As(err, &chain) {
		t.Fatalf("error = %v, want ChainError", err)
	}
	if failing.calls != before {
		t.Fatalf("cooling provider must be skipped, calls=%d want %d", failing.calls, before)
	}
	if len(chain.Attempts) == 0 || chain.Attempts[0].Status != "cooldown" {
		t.Fatalf("attempts = %+v", chain.Attempts)
	}
}

func TestAggregatorExplicitProviderBypassesCooldownAndFilters(t *testing.T) {
	failing := &stubProvider{name: "flaky", capabilities: Capabilities{Current: true}, err: errors.New("upstream timeout")}
	aggregator := testAggregator(failing)
	for attempt := 0; attempt < healthCooldownAfterFailures; attempt++ {
		_, _ = aggregator.Fetch(context.Background(), &WeatherRequest{Location: "Luoyang"})
	}
	before := failing.calls
	_, _ = aggregator.Fetch(context.Background(), &WeatherRequest{Location: "Luoyang", Provider: "flaky"})
	if failing.calls != before+1 {
		t.Fatalf("explicit provider must be attempted during cooldown, calls=%d want %d", failing.calls, before+1)
	}
	if _, err := aggregator.Fetch(context.Background(), &WeatherRequest{Location: "Luoyang", Provider: "missing"}); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("error = %v, want ErrProviderUnavailable", err)
	}
}

func TestAggregatorRejectsEmptyResponses(t *testing.T) {
	empty := &stubProvider{name: "empty", capabilities: Capabilities{Current: true}, response: &WeatherResponse{}}
	aggregator := testAggregator(empty)
	if _, err := aggregator.Fetch(context.Background(), &WeatherRequest{Location: "Luoyang"}); err == nil {
		t.Fatal("empty response must not be treated as success")
	}
}

func TestAggregatorRejectsInvalidRequests(t *testing.T) {
	aggregator := testAggregator(&stubProvider{name: "any", capabilities: Capabilities{Current: true}, response: &WeatherResponse{Current: &CurrentWeather{}}})
	for _, request := range []*WeatherRequest{
		{},
		{Location: "Luoyang", Days: 30},
		{Location: "Luoyang", Start: "2026/01/01", End: "2026-01-31"},
	} {
		if _, err := aggregator.Fetch(context.Background(), request); err == nil {
			t.Fatalf("request %+v must be rejected", request)
		}
	}
}

func TestHealthScoreDropsOnFailures(t *testing.T) {
	tracker := newHealthTracker()
	now := time.Now()
	for attempt := 0; attempt < 3; attempt++ {
		tracker.record("flaky", 500*time.Millisecond, false, now)
	}
	if score := tracker.get("flaky").score(); score >= 85 {
		t.Fatalf("score = %.1f, want degraded score", score)
	}
	if !tracker.get("flaky").cooling(now) {
		t.Fatal("provider must be cooling after three consecutive failures")
	}
	tracker.record("flaky", 100*time.Millisecond, true, now)
	if tracker.get("flaky").cooling(now) {
		t.Fatal("success must clear cooldown")
	}
}
