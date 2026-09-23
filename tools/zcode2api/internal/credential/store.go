// Package credential stores web-authorized upstream credentials for the ZCode sidecar.
package credential

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CredentialStore persists upstream credentials obtained via the built-in login flow.
type CredentialStore struct {
	Path    string
	mu      sync.RWMutex
	cred    Credential
	ok      bool
	modTime time.Time
	size    int64
}

func (c *CredentialStore) PathWithDefault() string {
	if c.Path != "" {
		return c.Path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "glm-zcode-2api", "credential.json")
}

// Load reads the persisted credential if it exists and is fresh enough.
func (c *CredentialStore) Load() (Credential, bool, error) {
	path := c.PathWithDefault()
	if path == "" {
		return Credential{}, false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Credential{}, false, nil
		}
		return Credential{}, false, err
	}
	c.mu.RLock()
	if c.ok && info.ModTime().Equal(c.modTime) && info.Size() == c.size {
		defer c.mu.RUnlock()
		return c.cred, true, nil
	}
	c.mu.RUnlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, false, err
	}
	var stored storedCredential
	if err := json.Unmarshal(data, &stored); err != nil {
		return Credential{}, false, err
	}
	if stored.APIKey == "" || stored.BaseURL == "" || stored.ProviderID == "" {
		return Credential{}, false, nil
	}
	cred := Credential{
		APIKey:     stored.APIKey,
		BaseURL:    stored.BaseURL,
		ProviderID: stored.ProviderID,
		Provider:   stored.Provider,
		Source:     path,
	}
	c.mu.Lock()
	c.cred, c.ok, c.modTime, c.size = cred, true, info.ModTime(), info.Size()
	c.mu.Unlock()
	return cred, true, nil
}

// Save overwrites the credential file atomically with mode 0600.
func (c *CredentialStore) Save(cred Credential) error {
	if cred.APIKey == "" || cred.BaseURL == "" || cred.ProviderID == "" {
		return errors.New("credential is incomplete")
	}
	path := c.PathWithDefault()
	if path == "" {
		return errors.New("credential path is not configured")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp-" + randomSuffix()
	data, err := json.Marshal(storedCredential{
		APIKey:     cred.APIKey,
		BaseURL:    cred.BaseURL,
		ProviderID: cred.ProviderID,
		Provider:   cred.Provider,
		SavedAt:    time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.cred, c.ok, c.modTime, c.size = cred, true, info.ModTime(), info.Size()
	c.mu.Unlock()
	return nil
}

// Clear removes the persisted credential and cached value.
func (c *CredentialStore) Clear() error {
	path := c.PathWithDefault()
	if path == "" {
		return nil
	}
	c.mu.Lock()
	c.cred, c.ok, c.modTime, c.size = Credential{}, false, time.Time{}, 0
	c.mu.Unlock()
	return os.Remove(path)
}

type storedCredential struct {
	APIKey     string    `json:"api_key"`
	BaseURL    string    `json:"base_url"`
	ProviderID string    `json:"provider_id"`
	Provider   string    `json:"provider"`
	SavedAt    time.Time `json:"saved_at"`
}

func randomSuffix() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
