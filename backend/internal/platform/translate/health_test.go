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

func TestHealthHealthyBeforeRecoveryProbe(t *testing.T) {
	a := &Aggregator{translators: []Translator{healthyTranslator("caiyun"), healthyTranslator("hymt")}}
	a.record("caiyun", time.Second, false, time.Now().Add(-31*time.Second))
	// 健康服务先消耗请求预算：恢复探测若排在前面，慢服务会用满单次超时拖垮整个请求。
	if a.rankedProviders(time.Now())[0].Name() != "hymt" {
		t.Fatal("healthy provider must outrank a recovering probe")
	}
	// 健康候选不可用时，恢复窗口到期的失败服务仍会获得探测机会，不会永久饿死。
	a.translators[1] = healthTestTranslator{name: "hymt", run: func(context.Context, *TranslateRequest) (*TranslateResponse, error) {
		return nil, errors.New("hymt unavailable")
	}}
	resp, err := a.Translate(context.Background(), healthRequest())
	if err != nil || resp.Provider != "caiyun" {
		t.Fatalf("recovery probe missing: %+v %v", resp, err)
	}
}

func TestHealthRecoveringTimeoutDoesNotPrecedeHealthy(t *testing.T) {
	// 复现线上故障：慢的恢复探测排在健康服务之前，把整次翻译拖到超时。
	slowProbe := healthTestTranslator{name: "caiyun", run: func(ctx context.Context, _ *TranslateRequest) (*TranslateResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	a := &Aggregator{translators: []Translator{slowProbe, healthyTranslator("libretranslate")}}
	a.record("caiyun", time.Second, false, time.Now().Add(-31*time.Second))
	resp, err := a.Translate(context.Background(), healthRequest())
	if err != nil || resp.Provider != "libretranslate" {
		t.Fatalf("healthy provider did not win: %+v %v", resp, err)
	}
	if len(resp.Attempts) != 1 {
		t.Fatalf("slow probe wasted the request budget: %+v", resp.Attempts)
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

func TestProviderAttemptBudgetLeavesHeadroom(t *testing.T) {
	// 无调用方期限时使用固定预算，必须明显小于 Host 的 15 秒整体超时。
	if budget := providerAttemptBudget(context.Background(), "caiyun", 1); budget != 5*time.Second {
		t.Fatalf("unexpected budget: %s", budget)
	}
	// 调用方期限更小时必须继续向下取小，避免单次尝试吃光剩余时间。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if budget := providerAttemptBudget(ctx, "caiyun", 1); budget > 2*time.Second {
		t.Fatalf("budget ignored caller deadline: %s", budget)
	}
}

func TestProviderAttemptBudgetBoundsSlowCandidate(t *testing.T) {
	// 只有卡死候选可用时，单次尝试必须受预算约束，不能无限占用调用方时间。
	slow := healthTestTranslator{name: "caiyun", run: func(ctx context.Context, _ *TranslateRequest) (*TranslateResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	a := &Aggregator{translators: []Translator{slow}}
	started := time.Now()
	_, err := a.Translate(context.Background(), healthRequest())
	if err == nil {
		t.Fatal("slow chain unexpectedly succeeded")
	}
	elapsed := time.Since(started)
	// 预算 5 秒，明显小于此前的 10 秒，为后续候选留出降级余量。
	if elapsed > 8*time.Second {
		t.Fatalf("attempt budget too large: %s", elapsed)
	}
	if elapsed < 3*time.Second {
		t.Fatalf("slow candidate returned before its budget elapsed: %s", elapsed)
	}
}

func TestProviderAttemptBudgetShrinksToCallerDeadline(t *testing.T) {
	slow := healthTestTranslator{name: "hymt", run: func(ctx context.Context, _ *TranslateRequest) (*TranslateResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	a := &Aggregator{translators: []Translator{slow}}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _ = a.Translate(ctx, healthRequest())
	elapsed := time.Since(started)
	// 调用方期限更小时必须继续向下取小，不能套用 5 秒固定预算。
	if elapsed >= 3*time.Second {
		t.Fatalf("budget ignored caller deadline: %s", elapsed)
	}
}

func TestOfflineProvidersNeverCooldown(t *testing.T) {
	for _, name := range []string{"libretranslate", "hymt"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			a := &Aggregator{translators: []Translator{healthTestTranslator{name: name, run: func(context.Context, *TranslateRequest) (*TranslateResponse, error) {
				calls++
				return nil, errors.New("upstream timeout")
			}}}}
			for i := 0; i < 6; i++ {
				_, _ = a.Translate(context.Background(), healthRequest())
			}
			if calls != 6 {
				t.Fatalf("offline requests skipped: %d", calls)
			}
			h := a.ProviderHealth()[0]
			if h.CooldownUntil != nil || h.ConsecutiveFailures != 6 {
				t.Fatalf("unexpected health: %+v", h)
			}
			// 即使有遗留的探测标记，也不能阻止并发离线请求。
			a.health[name].probing = true
			a.translators[0] = healthyTranslator(name)
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := a.Translate(context.Background(), healthRequest()); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if a.ProviderHealth()[0].Successes != 8 {
				t.Fatal("offline recovery blocked")
			}
		})
	}
}

func TestOfflineBatchBudget(t *testing.T) {
	for _, name := range []string{"libretranslate", "hymt"} {
		for items, want := range map[int]time.Duration{1: 30 * time.Second, 3: 90 * time.Second, 8: 120 * time.Second} {
			if got := providerAttemptBudget(context.Background(), name, items); got != want {
				t.Fatalf("%s batch %d: %s", name, items, got)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if got := providerAttemptBudget(ctx, name, 8); got > time.Second {
			t.Fatalf("ignored caller deadline: %s", got)
		}
		cancel()
	}
}
