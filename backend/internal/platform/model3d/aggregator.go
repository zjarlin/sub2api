package model3d

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// defaultPriority 是未显式配置 MODEL3D_PRIORITY 时的自动选择顺序。
//
// 文生 3D 优先 Meshy（两阶段 preview/refine，产物格式最全），图生 3D 优先 Meshy，
// 其后 Tripo；Hi3D 只处理图生 3D 且需要上传图片，放在最后作为补充。
var defaultPriority = []string{"meshy", "tripo", "hi3d"}

// Aggregator 聚合多个 3D 服务商，负责可用性判断与自动选择。
type Aggregator struct {
	providers []Provider
	byKey     map[string]Provider
	enabled   bool
}

// NewAggregator 按配置构建聚合器；未配置任何凭据时聚合器保持关闭状态。
func NewAggregator(cfg *Config) *Aggregator {
	aggregator := &Aggregator{byKey: map[string]Provider{}}
	if cfg == nil || !cfg.Configured() {
		return aggregator
	}
	aggregator.enabled = true
	timeout := cfg.TimeoutSeconds
	providers := map[string]Provider{
		"meshy": NewMeshyProvider(cfg.Meshy, timeout),
		"tripo": NewTripoProvider(cfg.Tripo, timeout),
		"hi3d":  NewHi3DProvider(cfg.Hi3D, timeout),
	}
	if cfg.ProxyURL != "" {
		proxy, err := url.Parse(cfg.ProxyURL)
		if err != nil || proxy.Host == "" {
			return &Aggregator{byKey: map[string]Provider{}}
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(proxy)
		for _, provider := range providers {
			switch typed := provider.(type) {
			case *MeshyProvider:
				typed.client.Transport = transport
			case *TripoProvider:
				typed.client.Transport = transport
			case *Hi3DProvider:
				typed.client.Transport = transport
			}
		}
	}
	order := orderedPriority(cfg.Priority)
	for _, key := range order {
		provider, ok := providers[key]
		if !ok {
			continue
		}
		aggregator.providers = append(aggregator.providers, provider)
		aggregator.byKey[key] = provider
	}
	if len(aggregator.providers) == 0 {
		aggregator.enabled = false
	}
	return aggregator
}

// orderedPriority 把配置的优先级展开成完整的服务商顺序，未列出的按默认顺序补齐。
func orderedPriority(priority []string) []string {
	seen := map[string]bool{}
	order := make([]string, 0, len(defaultPriority))
	for _, raw := range priority {
		key := strings.ToLower(strings.TrimSpace(raw))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		order = append(order, key)
	}
	for _, key := range defaultPriority {
		if seen[key] {
			continue
		}
		seen[key] = true
		order = append(order, key)
	}
	return order
}

// Enabled 报告聚合器是否已配置至少一个服务商。
func (a *Aggregator) Enabled() bool {
	return a != nil && a.enabled
}

// ProviderKeys 返回已注册的服务商 key（按优先级顺序）。
func (a *Aggregator) ProviderKeys() []string {
	if a == nil {
		return nil
	}
	keys := make([]string, 0, len(a.providers))
	for _, provider := range a.providers {
		keys = append(keys, provider.Key())
	}
	return keys
}

// ProviderInfos 返回全部已注册服务商的能力快照（含未配置凭据的）。
func (a *Aggregator) ProviderInfos() []ProviderInfo {
	if a == nil {
		return nil
	}
	infos := make([]ProviderInfo, 0, len(a.providers))
	for index, provider := range a.providers {
		info := provider.Info()
		info.Default = index == 0
		infos = append(infos, info)
	}
	return infos
}

// AvailableProviders 返回已配置凭据、可以真正调用的服务商 key。
func (a *Aggregator) AvailableProviders() []string {
	if a == nil {
		return nil
	}
	available := make([]string, 0, len(a.providers))
	for _, provider := range a.providers {
		if provider.Info().Configured {
			available = append(available, provider.Key())
		}
	}
	return available
}

// ProviderHealth 返回服务商静态可用性（不发起网络请求），供控制台展示。
func (a *Aggregator) ProviderHealth() map[string]string {
	health := map[string]string{}
	if a == nil {
		return health
	}
	for _, provider := range a.providers {
		if provider.Info().Configured {
			health[provider.Key()] = "ready"
		} else {
			health[provider.Key()] = "unconfigured"
		}
	}
	return health
}

// Provider 按 key 取服务商；未注册或未配置凭据时返回 ErrProviderUnavailable。
func (a *Aggregator) Provider(key string) (Provider, error) {
	if a == nil {
		return nil, ErrNoProvider
	}
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return nil, ErrProviderUnavailable
	}
	provider, ok := a.byKey[normalized]
	if !ok {
		return nil, ErrProviderUnavailable
	}
	if !provider.Info().Configured {
		return nil, ErrProviderUnavailable
	}
	return provider, nil
}

// Candidates 返回支持该输入形态、且已配置凭据的服务商（按优先级顺序）。
func (a *Aggregator) Candidates(mode Mode) []Provider {
	if a == nil {
		return nil
	}
	candidates := make([]Provider, 0, len(a.providers))
	for _, provider := range a.providers {
		info := provider.Info()
		if !info.Configured || !info.Supports(mode) {
			continue
		}
		candidates = append(candidates, provider)
	}
	return candidates
}

// Create 创建任务。
//
// 显式指定 provider 时只调用该服务商；未指定时按优先级选择第一个支持该形态的服务商。
// 这里刻意不做跨服务商自动重试：创建请求一旦到达上游就可能已经计费，
// 失败后换一家重试会造成重复扣费与重复任务。
func (a *Aggregator) Create(ctx context.Context, req *CreateRequest) (*Task, error) {
	if req == nil {
		return nil, ErrInvalidRequest
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	mode := req.Mode.Normalize()

	var provider Provider
	if strings.TrimSpace(req.Provider) != "" {
		selected, err := a.Provider(req.Provider)
		if err != nil {
			return nil, err
		}
		if !selected.Info().Supports(mode) {
			return nil, ErrUnsupported
		}
		provider = selected
	} else {
		candidates := a.Candidates(mode)
		if len(candidates) == 0 {
			return nil, ErrNoProvider
		}
		provider = candidates[0]
	}

	taskID, err := provider.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	return &Task{
		ID:             taskID,
		Mode:           mode,
		Provider:       provider.Key(),
		Model:          strings.TrimSpace(req.Model),
		ProviderStatus: "created",
		Status:         StatusQueued,
		Prompt:         strings.TrimSpace(req.Prompt),
	}, nil
}

// modeAwareProvider 是需要输入形态才能定位上游任务的服务商（Meshy 文生/图生路径不同）。
type modeAwareProvider interface {
	GetWithMode(ctx context.Context, mode Mode, taskID string) (*Task, error)
	CancelWithMode(ctx context.Context, mode Mode, taskID string) error
}

// Get 查询任务。providerKey 与 mode 来自网关签发的任务句柄。
func (a *Aggregator) Get(ctx context.Context, providerKey string, mode Mode, taskID string) (*Task, error) {
	provider, err := a.Provider(providerKey)
	if err != nil {
		return nil, err
	}
	var task *Task
	if aware, ok := provider.(modeAwareProvider); ok {
		task, err = aware.GetWithMode(ctx, mode, taskID)
	} else {
		task, err = provider.Get(ctx, taskID)
	}
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, &UpstreamError{Provider: provider.Key(), Message: "empty task response"}
	}
	task.Provider = provider.Key()
	if task.Mode == "" {
		task.Mode = mode.Normalize()
	}
	return task, nil
}

// Cancel 取消任务；服务商不支持时返回 ErrUnsupported。
func (a *Aggregator) Cancel(ctx context.Context, providerKey string, mode Mode, taskID string) error {
	provider, err := a.Provider(providerKey)
	if err != nil {
		return err
	}
	if aware, ok := provider.(modeAwareProvider); ok {
		return aware.CancelWithMode(ctx, mode, taskID)
	}
	return provider.Cancel(ctx, taskID)
}

// Balance 查询指定服务商的账户余额；providerKey 为空时使用默认服务商。
func (a *Aggregator) Balance(ctx context.Context, providerKey string) (string, float64, error) {
	var provider Provider
	var err error
	if strings.TrimSpace(providerKey) == "" {
		if a == nil || len(a.providers) == 0 {
			return "", 0, ErrNoProvider
		}
		provider = a.providers[0]
	} else {
		provider, err = a.Provider(providerKey)
		if err != nil {
			return "", 0, err
		}
	}
	if !provider.Info().Configured {
		return provider.Key(), 0, ErrProviderUnavailable
	}
	balance, err := provider.Balance(ctx)
	if err != nil {
		return provider.Key(), 0, err
	}
	return provider.Key(), balance, nil
}
