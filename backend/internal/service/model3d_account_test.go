package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/platform/model3d"
	"github.com/stretchr/testify/require"
)

type model3DRepoStub struct {
	AccountRepository
	accounts      []Account
	selectedGroup int64
}

func (r *model3DRepoStub) ListSchedulableByGroupIDAndPlatform(_ context.Context, group int64, platform string) ([]Account, error) {
	r.selectedGroup = group
	return r.accounts, nil
}
func (r *model3DRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			return &r.accounts[i], nil
		}
	}
	return nil, ErrAccountNotFound
}

// model3DAccountFixture 提供绑定分组且具有真实凭据字段的测试账号。
func model3DAccountFixture(id int64, provider string) Account {
	return Account{ID: id, Platform: PlatformModel3D, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{9}, Credentials: map[string]any{"model3d_provider": provider, "api_key": "private", "client_id": "client", "client_secret": "secret"}}
}

// TestModel3DPoolSelectionAndTaskOwnership 确认分组隔离、服务商能力过滤、账号停调后查询和身份变更拒绝。
func TestModel3DPoolSelectionAndTaskOwnership(t *testing.T) {
	group := int64(9)
	key := &APIKey{GroupID: &group}
	repo := &model3DRepoStub{accounts: []Account{model3DAccountFixture(1, "hi3d"), model3DAccountFixture(2, "meshy")}}
	pool := NewModel3DAccountPool(repo, nil)
	account, provider, release, err := pool.Select(context.Background(), key, &model3d.CreateRequest{Mode: model3d.ModeTextTo3D})
	require.NoError(t, err)
	defer release()
	require.Equal(t, int64(2), account.ID)
	require.Equal(t, "meshy", provider.Key())
	require.Equal(t, group, repo.selectedGroup)
	handle := model3d.TaskHandle{Provider: "meshy", AccountID: 2, GroupID: group, CredentialIdentity: Model3DAccountIdentity(account)}
	repo.accounts[1].Schedulable = false
	_, _, err = pool.Resolve(context.Background(), handle, key)
	require.NoError(t, err)
	foreignGroup := int64(10)
	_, _, err = pool.Resolve(context.Background(), handle, &APIKey{GroupID: &foreignGroup})
	require.ErrorIs(t, err, model3d.ErrTaskMismatch)
	repo.accounts[1].Credentials["api_key"] = "rotated"
	_, _, err = pool.Resolve(context.Background(), handle, key)
	require.ErrorIs(t, err, model3d.ErrTaskMismatch)
}

// TestModel3DClientSecretValidationAndPreservation 编辑未提供密钥时保留，响应剥离 Hi3D 密钥。
func TestModel3DClientSecretValidationAndPreservation(t *testing.T) {
	account := model3DAccountFixture(1, "hi3d")
	require.NoError(t, ValidateModel3DAccount(account.Platform, account.Type, account.Credentials))
	require.Error(t, ValidateModel3DAccount(account.Platform, AccountTypeOAuth, account.Credentials))
	merged := MergePreservingSensitiveCreds(account.Credentials, map[string]any{"model3d_provider": "hi3d", "client_id": "client"})
	require.Equal(t, "secret", merged["client_secret"])
	require.True(t, IsSensitiveCredentialKey("client_secret"))
}

// TestModel3DImageTaskUsesMeshyImagePath 图生任务的查询和删除不能落入文生路径。
func TestModel3DImageTaskUsesMeshyImagePath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/image-to-3d/id", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":"id","status":"SUCCEEDED"}`))
		}
	}))
	defer server.Close()
	account := model3DAccountFixture(1, "meshy")
	account.Credentials["base_url"] = server.URL
	provider, err := Model3DAccountProvider(&account)
	require.NoError(t, err)
	handle := model3d.TaskHandle{TaskID: "id", Mode: model3d.ModeImageTo3D}
	_, err = GetModel3DAccountTask(context.Background(), provider, handle)
	require.NoError(t, err)
	require.NoError(t, CancelModel3DAccountTask(context.Background(), provider, handle))
}

func (r *model3DRepoStub) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	accounts := make([]*Account, 0, len(r.accounts))
	for i := range r.accounts {
		accounts = append(accounts, &r.accounts[i])
	}
	return accounts, nil
}

// TestModel3DBulkRejectsInvalidIdentity 批量编辑在首次写入前拒绝服务商切换和空密钥。
func TestModel3DBulkRejectsInvalidIdentity(t *testing.T) {
	for _, credentials := range []map[string]any{{"model3d_provider": "tripo"}, {"client_secret": ""}} {
		repo := &model3DRepoStub{accounts: []Account{model3DAccountFixture(1, "hi3d")}}
		s := &adminServiceImpl{accountRepo: repo}
		_, err := s.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Credentials: credentials})
		require.Error(t, err)
	}
}

// TestModel3DPinnedAccountSelection 精修请求固定原始账号，不能切换到优先级更高的账号。
func TestModel3DPinnedAccountSelection(t *testing.T) {
	group := int64(9)
	repo := &model3DRepoStub{accounts: []Account{model3DAccountFixture(1, "meshy"), model3DAccountFixture(2, "meshy")}}
	pool := NewModel3DAccountPool(repo, nil)
	a, _, release, err := pool.Select(context.Background(), &APIKey{GroupID: &group}, &model3d.CreateRequest{AccountID: 2, Mode: model3d.ModeTextTo3D})
	require.NoError(t, err)
	defer release()
	require.Equal(t, int64(2), a.ID)
}

// TestModel3DAccountProxy 余额探测确实经过账号代理，不直接访问上游。
func TestModel3DAccountProxy(t *testing.T) {
	calls := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "upstream.invalid", r.URL.Host)
		_, _ = w.Write([]byte(`{"balance":12}`))
	}))
	defer proxy.Close()
	parsed, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(parsed.Port())
	require.NoError(t, err)
	a := model3DAccountFixture(1, "meshy")
	a.Credentials["base_url"] = "http://upstream.invalid"
	id := int64(8)
	a.ProxyID = &id
	a.Proxy = &Proxy{ID: id, Protocol: "http", Host: parsed.Hostname(), Port: port}
	provider, err := Model3DAccountProvider(&a)
	require.NoError(t, err)
	balance, err := provider.Balance(context.Background())
	require.NoError(t, err)
	require.Equal(t, float64(12), balance)
	require.Equal(t, 1, calls)
}
