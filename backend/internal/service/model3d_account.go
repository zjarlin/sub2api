package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/platform/model3d"
)

// ValidateModel3DAccount 校验服务商和机器凭据，不允许将网页或 OAuth 会话用于官方接口。
func ValidateModel3DAccount(platform, accountType string, credentials map[string]any) error {
	if platform != PlatformModel3D {
		return nil
	}
	if accountType != AccountTypeAPIKey {
		return infraerrors.BadRequest("MODEL3D_ACCOUNT_INVALID", "3D accounts require apikey credentials")
	}
	value := func(key string) string { v, _ := credentials[key].(string); return strings.TrimSpace(v) }
	if base := value("base_url"); base != "" {
		parsed, err := url.Parse(base)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return infraerrors.BadRequest("MODEL3D_ACCOUNT_INVALID", "Invalid 3D upstream URL")
		}
	}
	switch value("model3d_provider") {
	case "meshy", "tripo":
		if value("api_key") == "" {
			return infraerrors.BadRequest("MODEL3D_ACCOUNT_INVALID", "3D provider API key is required")
		}
	case "hi3d":
		if value("client_id") == "" || value("client_secret") == "" {
			return infraerrors.BadRequest("MODEL3D_ACCOUNT_INVALID", "Hi3D client_id and client_secret are required")
		}
	default:
		return infraerrors.BadRequest("MODEL3D_ACCOUNT_INVALID", "model3d_provider must be meshy, tripo or hi3d")
	}
	return nil
}

// Model3DAccountIdentity 将上游身份绑定到句柄；只保存摘要，不暴露原始凭据。
func Model3DAccountIdentity(a *Account) string {
	values := []string{a.GetCredential("model3d_provider"), a.GetCredential("base_url"), a.GetCredential("api_key"), a.GetCredential("client_id"), a.GetCredential("client_secret")}
	payload, _ := json.Marshal(values)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// Model3DAccountProvider 从账号凭据创建官方接口适配器。
func Model3DAccountProvider(a *Account) (model3d.Provider, error) {
	return model3DAccountProvider(a, 60)
}

func model3DAccountProvider(a *Account, timeoutSeconds int) (model3d.Provider, error) {
	if a == nil || a.Platform != PlatformModel3D {
		return nil, model3d.ErrProviderUnavailable
	}
	if err := ValidateModel3DAccount(a.Platform, a.Type, a.Credentials); err != nil {
		return nil, err
	}
	c := &model3d.Config{Enabled: true, TimeoutSeconds: timeoutSeconds}
	if a.ProxyID != nil {
		if a.Proxy == nil {
			return nil, infraerrors.BadRequest("MODEL3D_PROXY_UNAVAILABLE", "3D account proxy is unavailable")
		}
		c.ProxyURL = a.Proxy.URL()
	}
	base, model := a.GetCredential("base_url"), a.GetCredential("default_model")
	defaults := map[string]string{"meshy": "https://api.meshy.ai/openapi", "tripo": "https://api.tripo3d.ai/v2/openapi", "hi3d": "https://api.hitem3d.ai"}
	models := map[string]string{"meshy": "meshy-5", "tripo": "v2.5-20250123", "hi3d": "hi3dv3.0"}
	if base == "" {
		base = defaults[a.GetCredential("model3d_provider")]
	}
	if model == "" {
		model = models[a.GetCredential("model3d_provider")]
	}
	switch a.GetCredential("model3d_provider") {
	case "meshy":
		c.Meshy = &model3d.MeshyConfig{APIKey: a.GetCredential("api_key"), BaseURL: base, DefaultModel: model}
	case "tripo":
		c.Tripo = &model3d.TripoConfig{APIKey: a.GetCredential("api_key"), BaseURL: base, DefaultModel: model}
	case "hi3d":
		c.Hi3D = &model3d.Hi3DConfig{ClientID: a.GetCredential("client_id"), ClientSecret: a.GetCredential("client_secret"), BaseURL: base, DefaultModel: model}
	}
	return model3d.NewAggregator(c).Provider(a.GetCredential("model3d_provider"))
}

// Model3DAccountPool 复用账号分组、优先级和并发槽位，缓存 Hi3D 令牌持有者。
type Model3DAccountPool struct {
	timeoutSeconds int
	repo           AccountRepository
	concurrency    *ConcurrencyService
	mu             sync.Mutex
	providers      map[int64]model3DProviderEntry
}
type model3DProviderEntry struct {
	identity string
	provider model3d.Provider
}

func NewModel3DAccountPool(repo AccountRepository, concurrency *ConcurrencyService) *Model3DAccountPool {
	return &Model3DAccountPool{repo: repo, concurrency: concurrency, providers: make(map[int64]model3DProviderEntry)}
}

// SetTimeoutSeconds 在启动期间设置上游请求超时。
func (p *Model3DAccountPool) SetTimeoutSeconds(seconds int) {
	p.timeoutSeconds = seconds
}

// Provider 账号凭据变化时替换缓存，确保令牌不会跨账号复用。
func (p *Model3DAccountPool) Provider(a *Account) (model3d.Provider, error) {
	payload, err := json.Marshal([]any{a.Credentials, a.ProxyID, a.Proxy})
	if err != nil {
		return nil, err
	}
	checksum := sha256.Sum256(payload)
	identity := hex.EncodeToString(checksum[:])
	p.mu.Lock()
	defer p.mu.Unlock()
	if cached, ok := p.providers[a.ID]; ok && cached.identity == identity {
		return cached.provider, nil
	}
	provider, err := model3DAccountProvider(a, p.timeoutSeconds)
	if err != nil {
		return nil, err
	}
	p.providers[a.ID] = model3DProviderEntry{identity: identity, provider: provider}
	return provider, nil
}

// Select 只从当前分组可调度账号中选择，提交上游后不再故障切换。
func (p *Model3DAccountPool) Select(ctx context.Context, key *APIKey, req *model3d.CreateRequest) (*Account, model3d.Provider, func(), error) {
	if key == nil || key.GroupID == nil {
		return nil, nil, nil, ErrNoAvailableAccounts
	}
	accounts, err := p.repo.ListSchedulableByGroupIDAndPlatform(ctx, *key.GroupID, PlatformModel3D)
	if err != nil {
		return nil, nil, nil, err
	}
	userRelease := func() {}
	if p.concurrency != nil && key.User != nil {
		userSlot, err := p.concurrency.AcquireUserSlot(ctx, key.UserID, max(1, key.User.Concurrency))
		if err != nil {
			return nil, nil, nil, err
		}
		if !userSlot.Acquired {
			return nil, nil, nil, ErrNoAvailableAccounts
		}
		userRelease = userSlot.ReleaseFunc
	}
	selected := false
	defer func() {
		if !selected {
			userRelease()
		}
	}()
	sort.SliceStable(accounts, func(i, j int) bool {
		pi, pj := model3DGroupPriority(&accounts[i], *key.GroupID), model3DGroupPriority(&accounts[j], *key.GroupID)
		if pi != pj {
			return pi < pj
		}
		if accounts[i].LastUsedAt == nil {
			return accounts[j].LastUsedAt != nil
		}
		if accounts[j].LastUsedAt == nil {
			return false
		}
		return accounts[i].LastUsedAt.Before(*accounts[j].LastUsedAt)
	})
	for i := range accounts {
		a := &accounts[i]
		if req.AccountID > 0 && req.AccountID != a.ID {
			continue
		}
		if !a.IsSchedulable() || (req.Provider != "" && req.Provider != a.GetCredential("model3d_provider")) {
			continue
		}
		provider, err := p.Provider(a)
		if err != nil || !provider.Info().Supports(req.Mode.Normalize()) {
			continue
		}
		if req.Model != "" && len(a.GetModelMapping()) > 0 && !a.IsModelSupported(req.Model) {
			continue
		}
		release := func() {}
		if p.concurrency != nil {
			slot, err := p.concurrency.AcquireAccountSlot(ctx, a.ID, max(1, a.Concurrency))
			if err != nil {
				return nil, nil, nil, err
			}
			if !slot.Acquired {
				continue
			}
			release = slot.ReleaseFunc
		}
		selected = true
		accountRelease := release
		return a, provider, func() { accountRelease(); userRelease() }, nil
	}
	return nil, nil, nil, ErrNoAvailableAccounts
}

// Resolve 查询固定账号，验证分组、身份和启用状态；忙碌不影响已有任务查询。
func (p *Model3DAccountPool) Resolve(ctx context.Context, handle model3d.TaskHandle, key *APIKey) (*Account, model3d.Provider, error) {
	if key.GroupID == nil || *key.GroupID != handle.GroupID || handle.AccountID <= 0 {
		return nil, nil, model3d.ErrTaskMismatch
	}
	a, err := p.repo.GetByID(ctx, handle.AccountID)
	if err != nil {
		return nil, nil, err
	}
	if a == nil {
		return nil, nil, model3d.ErrTaskMismatch
	}
	bound := false
	for _, group := range a.GroupIDs {
		if group == handle.GroupID {
			bound = true
		}
	}
	if !bound || !a.IsActive() || a.Platform != PlatformModel3D || a.GetCredential("model3d_provider") != handle.Provider || Model3DAccountIdentity(a) != handle.CredentialIdentity {
		return nil, nil, model3d.ErrTaskMismatch
	}
	provider, err := p.Provider(a)
	return a, provider, err
}

// GetModel3DAccountTask 保留 Meshy 文生与图生任务的不同查询路径。
func GetModel3DAccountTask(ctx context.Context, provider model3d.Provider, handle model3d.TaskHandle) (*model3d.Task, error) {
	if provider, ok := provider.(interface {
		GetWithMode(context.Context, model3d.Mode, string) (*model3d.Task, error)
	}); ok {
		return provider.GetWithMode(ctx, handle.Mode, handle.TaskID)
	}
	return provider.Get(ctx, handle.TaskID)
}

// CancelModel3DAccountTask 按 Meshy 形态选择正确的删除路径。
func CancelModel3DAccountTask(ctx context.Context, provider model3d.Provider, handle model3d.TaskHandle) error {
	if aware, ok := provider.(interface {
		CancelWithMode(context.Context, model3d.Mode, string) error
	}); ok {
		return aware.CancelWithMode(ctx, handle.Mode, handle.TaskID)
	}
	return provider.Cancel(ctx, handle.TaskID)
}

// model3DGroupPriority 分组内优先级覆盖账号全局优先级。
func model3DGroupPriority(a *Account, group int64) int {
	for _, binding := range a.AccountGroups {
		if binding.GroupID == group {
			return binding.Priority
		}
	}
	return a.Priority
}

// MarkUsed 更新最近使用时间，使相同优先级账号轮换。
func (p *Model3DAccountPool) MarkUsed(ctx context.Context, id int64) error {
	return p.repo.UpdateLastUsed(ctx, id)
}
