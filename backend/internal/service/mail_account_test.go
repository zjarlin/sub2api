package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func mailTestAccount() *Account {
	return &Account{ID: 4, Platform: PlatformMail, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{9}, Credentials: map[string]any{"email": "test@gmail.com", "mail_provider": "google", "api_key": "secret"}}
}

func TestMailAccountBodyUsesSavedIdentityAndProxy(t *testing.T) {
	a := mailTestAccount()
	id := int64(1)
	a.ProxyID = &id
	a.Proxy = &Proxy{ID: id, Protocol: "socks5", Host: "proxy.test", Port: 1080, Status: StatusActive, Username: "user", Password: "p@ss"}
	body := map[string]any{"account_id": 4, "email": "other@test.test", "credential": "other", "proxy": "http://attacker:80", "imap_host": "attacker", "to": []string{"target@test.test"}}
	require.NoError(t, MailAccountBody(a, body))
	require.Equal(t, "test@gmail.com", body["email"])
	require.Equal(t, "secret", body["credential"])
	require.Equal(t, "socks5://user:p%40ss@proxy.test:1080", body["proxy"])
	require.NotContains(t, body, "imap_host")
	require.NotContains(t, body, "account_id")
	require.Equal(t, []string{"target@test.test"}, body["to"])
	require.False(t, a.IsSchedulable())
	a.Proxy = nil
	require.Error(t, MailAccountBody(a, map[string]any{}))
	a.ProxyID = nil
	body = map[string]any{"proxy": "http://attacker:80"}
	require.NoError(t, MailAccountBody(a, body))
	require.NotContains(t, body, "proxy")
	a.Status = "inactive"
	require.Error(t, MailAccountBody(a, map[string]any{}))
}

type mailAccountRepo struct {
	AccountRepository
	account *Account
}

func (r mailAccountRepo) GetByID(context.Context, int64) (*Account, error) { return r.account, nil }
func TestMailAccountGroupAuthorization(t *testing.T) {
	s := &GatewayService{accountRepo: mailAccountRepo{account: mailTestAccount()}}
	_, err := s.ResolveMailAccountBody(context.Background(), 4, 10, map[string]any{})
	require.Error(t, err)
	body := map[string]any{}
	account, err := s.ResolveMailAccountBody(context.Background(), 4, 9, body)
	require.NoError(t, err)
	require.Equal(t, int64(4), account.ID)
	require.Equal(t, "secret", body["credential"])
}
func TestMailAccountValidation(t *testing.T) {
	a := mailTestAccount()
	require.NoError(t, ValidateMailAccount(a.Platform, a.Type, a.Credentials))
	a.Credentials["email"] = "Name <test@gmail.com>"
	require.Error(t, ValidateMailAccount(a.Platform, a.Type, a.Credentials))
	a.Credentials["email"] = "test@gmail.com"
	a.Credentials["mail_provider"] = "unknown"
	require.Error(t, ValidateMailAccount(a.Platform, a.Type, a.Credentials))
}
