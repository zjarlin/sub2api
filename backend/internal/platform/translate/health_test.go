package translate

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type healthTestTranslator struct {
	name string
	run  func(context.Context, *TranslateRequest) (*TranslateResponse, error)
}

func (t healthTestTranslator) Name() string { return t.name }
func (t healthTestTranslator) DetectLanguage(context.Context, string) (string, error) {
	return "en", nil
}
func (t healthTestTranslator) Translate(ctx context.Context, r *TranslateRequest) (*TranslateResponse, error) {
	return t.run(ctx, r)
}
func healthyTranslator(name string) Translator {
	return healthTestTranslator{name: name, run: func(context.Context, *TranslateRequest) (*TranslateResponse, error) {
		return &TranslateResponse{Translations: []TranslationResult{{Text: "你好"}}}, nil
	}}
}
func healthRequest() *TranslateRequest {
	return &TranslateRequest{Text: []string{"Hello"}, SourceLang: "en", TargetLang: "zh-CN"}
}

func TestHealthFreeFirstAndBaiduLast(t *testing.T) {
	a := &Aggregator{translators: []Translator{healthyTranslator("baidu"), healthyTranslator("tencent"), healthyTranslator("hymt")}}
	a.record("hymt", time.Second, false, time.Now())
	resp, err := a.Translate(context.Background(), healthRequest())
	if err != nil || resp.Provider != "hymt" {
		t.Fatalf("free priority lost: %+v %v", resp, err)
	}
	names := a.AvailableProviders()
	if names[2] != "baidu" {
		t.Fatal(names)
	}
}

func TestHealthEmptyFallbackAndCooldownRecovery(t *testing.T) {
	failing := healthTestTranslator{name: "caiyun", run: func(context.Context, *TranslateRequest) (*TranslateResponse, error) { return &TranslateResponse{}, nil }}
	a := &Aggregator{translators: []Translator{failing, healthyTranslator("baidu")}}
	req := healthRequest()
	req.Provider = "caiyun"
	for i := 0; i < 3; i++ {
		if _, err := a.Translate(context.Background(), req); err == nil {
			t.Fatal("empty result accepted")
		}
	}
	req.Provider = ""
	resp, err := a.Translate(context.Background(), req)
	if err != nil || resp.Provider != "baidu" || resp.Attempts[0].Status != "cooldown" {
		t.Fatalf("cooldown fallback: %+v %v", resp, err)
	}
	a.mu.Lock()
	a.health["caiyun"].cooldownUntil = time.Now().Add(-time.Second)
	a.health["caiyun"].retryAt = time.Now().Add(-time.Second)
	a.mu.Unlock()
	a.translators[0] = healthyTranslator("caiyun")
	resp, err = a.Translate(context.Background(), req)
	if err != nil || resp.Provider != "caiyun" || a.ProviderHealth()[0].ConsecutiveFailures != 0 {
		t.Fatalf("recovery: %+v %v", resp, err)
	}
}

func TestHealthScoreReordersSameTier(t *testing.T) {
	a := &Aggregator{translators: []Translator{healthyTranslator("caiyun"), healthyTranslator("hymt")}}
	a.record("caiyun", 5*time.Second, false, time.Now())
	resp, err := a.Translate(context.Background(), healthRequest())
	if err != nil || resp.Provider != "hymt" {
		t.Fatalf("health ranking: %+v %v", resp, err)
	}
}

func TestHealthExplicitNoFallbackAndCancellation(t *testing.T) {
	a := &Aggregator{translators: []Translator{healthyTranslator("baidu"), healthTestTranslator{name: "hymt", run: func(context.Context, *TranslateRequest) (*TranslateResponse, error) { return nil, errors.New("failed") }}}}
	req := healthRequest()
	req.Provider = "hymt"
	_, err := a.Translate(context.Background(), req)
	var chain *ChainError
	if !errors.As(err, &chain) || len(chain.Attempts) != 1 {
		t.Fatalf("explicit fell back: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = a.Translate(ctx, healthRequest()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, health := range a.ProviderHealth() {
		if health.Provider == "baidu" && health.Attempts != 0 {
			t.Fatal("cancellation changed health")
		}
		if health.Provider == "hymt" && health.Attempts != 1 {
			t.Fatal("cancellation changed health")
		}
	}
}

func TestHealthConcurrentSnapshots(t *testing.T) {
	a := &Aggregator{translators: []Translator{healthyTranslator("hymt")}}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = a.Translate(context.Background(), healthRequest())
			_ = a.ProviderHealth()
		}()
	}
	wg.Wait()
	if a.ProviderHealth()[0].Successes != 20 {
		t.Fatal(a.ProviderHealth())
	}
}

func TestHealthRejectsMissingAndBlankItems(t *testing.T) {
	req := healthRequest()
	for _, resp := range []*TranslateResponse{nil, {}, {Translations: []TranslationResult{{Text: " "}}}} {
		if validateTranslation(req, resp) == nil {
			t.Fatal("invalid result accepted")
		}
	}
	req.Text = []string{" "}
	if err := validateTranslation(req, &TranslateResponse{Translations: []TranslationResult{{Text: ""}}}); err != nil {
		t.Fatal(err)
	}
}

func TestHealthSingleFailureGetsRecoveryProbe(t *testing.T) {
	a := &Aggregator{translators: []Translator{healthyTranslator("caiyun"), healthyTranslator("hymt")}}
	a.record("caiyun", time.Second, false, time.Now().Add(-31*time.Second))
	if a.rankedProviders(time.Now())[0].Name() != "caiyun" {
		t.Fatal("failed provider starved")
	}
	if a.cooling("caiyun", time.Now()) || !a.cooling("caiyun", time.Now()) {
		t.Fatal("recovery probe not exclusive")
	}
	a.releaseProbe("caiyun")
	resp, err := a.Translate(context.Background(), healthRequest())
	if err != nil || resp.Provider != "caiyun" {
		t.Fatalf("recovery failed: %+v %v", resp, err)
	}
}

func TestHealthCandidateTimeoutFallsBack(t *testing.T) {
	waiting := healthTestTranslator{name: "caiyun", run: func(ctx context.Context, _ *TranslateRequest) (*TranslateResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	a := &Aggregator{translators: []Translator{waiting, healthyTranslator("baidu")}}
	resp, err := a.Translate(context.Background(), healthRequest())
	if err != nil || resp.Provider != "baidu" || resp.Attempts[0].Status != "failed" {
		t.Fatalf("timeout fallback: %+v %v", resp, err)
	}
}
