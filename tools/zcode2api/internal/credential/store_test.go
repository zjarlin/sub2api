package credential

import (
	"path/filepath"
	"testing"
)

func TestCredentialStoreRoundTrip(t *testing.T) {
	store := &CredentialStore{Path: filepath.Join(t.TempDir(), "nested", "credential.json")}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Fatalf("empty store load = %v, %v", ok, err)
	}
	want := Credential{APIKey: "key.secret", BaseURL: "https://open.bigmodel.cn/api/anthropic", ProviderID: "builtin:bigmodel-coding-plan", Provider: "me@example.com", Source: "builtin-login"}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := store.Load()
	if err != nil || !ok {
		t.Fatalf("Load after save = %v, %v", ok, err)
	}
	if got.APIKey != want.APIKey || got.BaseURL != want.BaseURL || got.ProviderID != want.ProviderID {
		t.Fatalf("credential mismatch: %+v", got)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok, _ := store.Load(); ok {
		t.Fatal("credential must be gone after Clear")
	}
}

func TestCredentialStoreRejectsIncomplete(t *testing.T) {
	store := &CredentialStore{Path: filepath.Join(t.TempDir(), "credential.json")}
	if err := store.Save(Credential{APIKey: "only-key"}); err == nil {
		t.Fatal("incomplete credential must not be persisted")
	}
}
