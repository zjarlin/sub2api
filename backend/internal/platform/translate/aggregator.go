package translate

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

var ErrProviderUnavailable = errors.New("translate: provider is not configured")

// Aggregator 翻译服务聚合器，按优先级调用多个 Translator，失败自动回退
type Aggregator struct {
	translators []Translator
	mu          sync.Mutex
	health      map[string]*providerHealth
}

// NewAggregator 保留既有上游顺序，新接入服务追加为备用。
func NewAggregator(cfg *Config) *Aggregator {
	var translators []Translator

	// 彩云小译：免密钥、免注册，作为默认可用源优先尝试。
	if cfg.Caiyun != nil && cfg.Caiyun.Token != "" {
		translators = append(translators, NewCaiyunTranslator(cfg.Caiyun))
		log.Println("[translate] registered provider: caiyun")
	} else if cfg.EnableFreeProviders {
		translators = append(translators, NewCaiyunTranslator(nil))
		log.Println("[translate] registered provider: caiyun (built-in token)")
	}

	if cfg.GoogleWeb != nil {
		translators = append(translators, NewGoogleWebTranslator(cfg.GoogleWeb))
		log.Println("[translate] registered provider: google_web")
	}

	if cfg.Tencent != nil && cfg.Tencent.SecretID != "" {
		translators = append(translators, NewTencentTranslator(cfg.Tencent))
		log.Println("[translate] registered provider: tencent")
	}
	if cfg.Baidu != nil && cfg.Baidu.AppID != "" {
		translators = append(translators, NewBaiduTranslator(cfg.Baidu))
		log.Println("[translate] registered provider: baidu")
	}
	if cfg.Youdao != nil && cfg.Youdao.AppKey != "" {
		translators = append(translators, NewYoudaoTranslator(cfg.Youdao))
		log.Println("[translate] registered provider: youdao")
	}
	if cfg.MyMemory != nil {
		translators = append(translators, NewMyMemoryTranslator(cfg.MyMemory))
		log.Println("[translate] registered provider: mymemory")
	}
	if cfg.LibreTranslate != nil && cfg.LibreTranslate.BaseURL != "" {
		translators = append(translators, NewLibreTranslateTranslator(cfg.LibreTranslate))
		log.Println("[translate] registered provider: libretranslate")
	}
	if cfg.HyMT != nil && cfg.HyMT.BaseURL != "" {
		translators = append(translators, NewHyMTTranslator(cfg.HyMT))
		log.Println("[translate] registered provider: hymt")
	}

	if len(cfg.Priority) > 0 {
		rank := make(map[string]int, len(cfg.Priority))
		for i, name := range cfg.Priority {
			rank[name] = i
		}
		priority := func(name string) int {
			if value, ok := rank[name]; ok {
				return value
			}
			return len(cfg.Priority)
		}
		sort.SliceStable(translators, func(i, j int) bool { return priority(translators[i].Name()) < priority(translators[j].Name()) })
	}
	return &Aggregator{translators: translators, health: make(map[string]*providerHealth)}
}

// Translate 按优先级尝试翻译，首个成功即返回
func (a *Aggregator) Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("translate: request is required")
	}
	if len(req.Text) == 0 || req.TargetLang == "" {
		return nil, fmt.Errorf("translate: text and target language are required")
	}
	candidates := a.rankedProviders(time.Now())
	if req.Provider != "" {
		candidates = nil
		for _, t := range a.translators {
			if t.Name() == req.Provider {
				candidates = append(candidates, t)
				break
			}
		}
		if len(candidates) == 0 {
			return nil, ErrProviderUnavailable
		}
	}
	attempts := make([]TranslationAttempt, 0, len(candidates))
	for _, t := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req.Provider == "" && a.cooling(t.Name(), time.Now()) {
			attempts = append(attempts, TranslationAttempt{Provider: t.Name(), Status: "cooldown", Reason: "temporarily unavailable after previous failures; recovery pending"})
			continue
		}
		started := time.Now()
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		resp, err := t.Translate(attemptCtx, req)
		cancel()
		elapsed := time.Since(started)
		if ctx.Err() != nil {
			a.releaseProbe(t.Name())
			return nil, ctx.Err()
		}
		if err == nil {
			err = validateTranslation(req, resp)
		}
		score := a.record(t.Name(), elapsed, err == nil, time.Now())
		status := "success"
		if err != nil {
			status = "failed"
		}
		attempts = append(attempts, TranslationAttempt{Provider: t.Name(), Status: status, LatencyMS: elapsed.Milliseconds(), Score: score, Reason: failureReason(err, req.Text)})
		if err != nil {
			log.Printf("[translate] provider=%s status=failed latency_ms=%d score=%.1f", t.Name(), elapsed.Milliseconds(), score)
			continue
		}
		resp.Provider = t.Name()
		resp.Attempts = attempts
		return resp, nil
	}
	return nil, &ChainError{Attempts: attempts}
}

// DetectLanguage 使用最高优先级的可用服务商检测语言
func (a *Aggregator) DetectLanguage(ctx context.Context, text string) (string, error) {
	if len(a.translators) == 0 {
		return "", fmt.Errorf("translate: no providers configured")
	}
	return a.translators[0].DetectLanguage(ctx, text)
}

// AvailableProviders 返回已注册的服务商名称列表
func (a *Aggregator) AvailableProviders() []string {
	providers := a.rankedProviders(time.Now())
	names := make([]string, len(providers))
	for i, t := range providers {
		names[i] = t.Name()
	}
	return names
}
