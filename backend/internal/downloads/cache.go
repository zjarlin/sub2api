package downloads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	WindowsInstallerFile = "ChatGPT-Installer.exe"
	MacOSInstallerFile   = "Codex.dmg"
	WindowsInstallerName = "Windows ChatGPT/Codex installer"
	MacOSInstallerName   = "macOS Codex installer"
)

type Artifact struct {
	Name     string
	Filename string
	URL      string
	MinBytes int64
}

var artifacts = []Artifact{
	{Name: WindowsInstallerName, Filename: WindowsInstallerFile, URL: "https://get.microsoft.com/installer/download/9PLM9XGG6VKS", MinBytes: 100 * 1024},
	{Name: MacOSInstallerName, Filename: MacOSInstallerFile, URL: "https://persistent.oaistatic.com/codex-app-prod/Codex.dmg", MinBytes: 100 * 1024 * 1024},
}

type Cache struct {
	dir       string
	client    *http.Client
	refresh   time.Duration
	mu        sync.Mutex
	startOnce sync.Once
}

func CacheDir(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" || dataDir == "." {
		dataDir = os.Getenv("DATA_DIR")
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "/app/data"
	}
	return filepath.Join(dataDir, "downloads")
}

func NewCache(dir string) *Cache {
	return &Cache{
		dir:     dir,
		client:  &http.Client{Timeout: 20 * time.Minute},
		refresh: 12 * time.Hour,
	}
}

func NewStaticCache(dir string) *Cache {
	return &Cache{dir: dir}
}

func (c *Cache) Start(ctx context.Context) {
	if c == nil || c.client == nil {
		return
	}
	c.startOnce.Do(func() {
		go c.loop(ctx)
	})
}

func (c *Cache) Path(filename string) string {
	if c == nil || filename == "" {
		return ""
	}
	return filepath.Join(c.dir, filepath.Base(filename))
}

func (c *Cache) Open(filename string) (*os.File, os.FileInfo, error) {
	path := c.Path(filename)
	if path == "" {
		return nil, nil, errors.New("download cache is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if info.IsDir() || info.Size() == 0 {
		file.Close()
		return nil, nil, fmt.Errorf("cached download %s is empty", filename)
	}
	return file, info, nil
}

func (c *Cache) loop(ctx context.Context) {
	c.Refresh(ctx)
	ticker := time.NewTicker(c.refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Refresh(ctx)
		}
	}
}

func (c *Cache) Refresh(ctx context.Context) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		log.Printf("[downloads] create cache directory %s: %v", c.dir, err)
		return
	}
	for _, artifact := range artifacts {
		if err := c.refreshArtifact(ctx, artifact); err != nil {
			log.Printf("[downloads] refresh %s: %v", artifact.Name, err)
		}
	}
}

func (c *Cache) refreshArtifact(ctx context.Context, artifact Artifact) error {
	target := c.Path(artifact.Filename)
	if info, err := os.Stat(target); err == nil && info.Size() >= artifact.MinBytes {
		// HEAD is cheap and avoids re-downloading large unchanged DMGs.
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, artifact.URL, nil)
		if err == nil {
			resp, headErr := c.client.Do(req)
			if headErr == nil {
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 && resp.ContentLength > 0 && resp.ContentLength == info.Size() {
					return nil
				}
			}
		}
	}

	tmp, err := os.CreateTemp(c.dir, artifact.Filename+".part-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		tmp.Close()
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		tmp.Close()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		tmp.Close()
		return fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if written < artifact.MinBytes {
		return fmt.Errorf("downloaded %d bytes, expected at least %d", written, artifact.MinBytes)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	log.Printf("[downloads] cached %s bytes=%d sha256=%s", artifact.Name, written, hex.EncodeToString(hash.Sum(nil)))
	return nil
}

func IsKnownFile(filename string) bool {
	switch strings.TrimSpace(filename) {
	case WindowsInstallerFile, MacOSInstallerFile:
		return true
	default:
		return false
	}
}
