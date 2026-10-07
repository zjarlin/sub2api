package downloads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	WindowsInstallerFile = "ChatGPT-Installer.exe"
	MacOSInstallerFile   = "Codex.dmg"
	WindowsInstallerName = "Windows ChatGPT/Codex installer"
	MacOSInstallerName   = "macOS Codex installer"

	manifestFileName = "manifest.json"
	lockFileName     = ".refresh.lock"
	partPrefix       = ".part-"
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

// Entry 记录单个安装包的同步状态，用于「随官网更新」的可校验证据与前端展示。
type Entry struct {
	Filename     string `json:"filename"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	Source       string `json:"source"` // upstream / upstream-not-modified / local
	SyncedAt     string `json:"synced_at,omitempty"`
}

type manifest struct {
	UpdatedAt string  `json:"updated_at"`
	Entries   []Entry `json:"entries"`
}

type Cache struct {
	dir       string
	client    *http.Client
	refresh   time.Duration
	artifacts []Artifact
	mu        sync.Mutex
	startOnce sync.Once

	// refreshable 区分真实定时刷新与静态缓存（测试注入）。静态缓存只读不刷。
	refreshable bool
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
		dir:         dir,
		client:      &http.Client{Timeout: 20 * time.Minute},
		refresh:     12 * time.Hour,
		artifacts:   artifacts,
		refreshable: true,
	}
}

// NewStaticCache 返回只读缓存，不会启动后台刷新，供测试与只读场景使用。
func NewStaticCache(dir string) *Cache {
	return &Cache{dir: dir, artifacts: artifacts}
}

func (c *Cache) Start(ctx context.Context) {
	if c == nil || c.client == nil || !c.refreshable {
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

// RefreshInterval 返回后台刷新周期，供接口对外说明「多久跟一次官网」。
func (c *Cache) RefreshInterval() time.Duration {
	if c == nil || c.refresh <= 0 {
		return 0
	}
	return c.refresh
}

// Manifest 返回最近一次刷新的同步状态；尚未生成时返回空条目，不报错。
func (c *Cache) Manifest() []Entry {
	return c.readManifest().Entries
}

// ManifestUpdatedAt 返回最近一次刷新周期的完成时间，用于「上次同步」展示。
func (c *Cache) ManifestUpdatedAt() string {
	return c.readManifest().UpdatedAt
}

func (c *Cache) readManifest() manifest {
	if c == nil {
		return manifest{}
	}
	raw, err := os.ReadFile(filepath.Join(c.dir, manifestFileName))
	if err != nil {
		return manifest{}
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return manifest{}
	}
	return m
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

// Refresh 同步所有安装包。用文件锁保证同一 data 卷上只有一个进程在刷新：
// 蓝绿部署期间候选容器与主容器共享卷，避免重复下载同一个大文件。
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
	release, ok := c.acquireLock()
	if !ok {
		log.Printf("[downloads] another instance is refreshing; skipping this cycle")
		return
	}
	defer release()

	c.cleanOrphanParts()

	entries := make([]Entry, 0, len(c.artifacts))
	for _, artifact := range c.artifacts {
		entry, err := c.refreshArtifact(ctx, artifact)
		if err != nil {
			log.Printf("[downloads] refresh %s: %v", artifact.Name, err)
			if prev := c.cachedEntry(artifact); prev != nil {
				entries = append(entries, *prev)
			}
			continue
		}
		entries = append(entries, *entry)
	}
	c.writeManifest(entries)
}

// acquireLock 尝试独占刷新锁。锁文件是常规文件，进程被杀后 fd 释放、锁自动失效。
func (c *Cache) acquireLock() (func(), bool) {
	path := filepath.Join(c.dir, lockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, false
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return func() {}, false
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, true
}

// cleanOrphanParts 删除刷新中断遗留的临时文件。
//
// 调用方必须已持有刷新锁：同一时刻只有一个进程在刷新，因此此刻目录里任何
// 下载分片都不可能是「正在写入」的，可以安全清除，避免大文件残留占用磁盘。
func (c *Cache) cleanOrphanParts() {
	matches, err := filepath.Glob(filepath.Join(c.dir, "*"+partPrefix+"*"))
	if err != nil {
		return
	}
	for _, path := range matches {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			continue
		}
		if err := os.Remove(path); err == nil {
			log.Printf("[downloads] removed orphaned partial %s", filepath.Base(path))
		}
	}
}

func (c *Cache) refreshArtifact(ctx context.Context, artifact Artifact) (*Entry, error) {
	target := c.Path(artifact.Filename)
	existing, statErr := os.Stat(target)
	haveCache := statErr == nil && existing.Size() >= artifact.MinBytes
	prev := c.cachedEntry(artifact)

	if haveCache {
		unchanged, err := c.upstreamUnchanged(ctx, artifact, existing, prev)
		if err != nil {
			log.Printf("[downloads] upstream check %s: %v", artifact.Name, err)
		} else if unchanged {
			entry := *prev
			entry.Source = "upstream-not-modified"
			entry.SyncedAt = time.Now().UTC().Format(time.RFC3339)
			// 升级前缓存的包没有哈希，这里一次性补算，保证清单始终可校验。
			if entry.SHA256 == "" {
				if sum, err := hashFile(target); err == nil {
					entry.SHA256 = sum
				} else {
					log.Printf("[downloads] hash %s: %v", artifact.Name, err)
				}
			}
			return &entry, nil
		}
	}

	tmp, err := os.CreateTemp(c.dir, "."+artifact.Filename+partPrefix+"*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		tmp.Close()
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		tmp.Close()
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		tmp.Close()
		return nil, fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if written < artifact.MinBytes {
		return nil, fmt.Errorf("downloaded %d bytes, expected at least %d", written, artifact.MinBytes)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return nil, err
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	log.Printf("[downloads] cached %s bytes=%d sha256=%s", artifact.Name, written, sum)
	return &Entry{
		Filename:     artifact.Filename,
		Name:         artifact.Name,
		URL:          artifact.URL,
		Size:         written,
		SHA256:       sum,
		ETag:         strings.TrimSpace(resp.Header.Get("ETag")),
		LastModified: strings.TrimSpace(resp.Header.Get("Last-Modified")),
		Source:       "upstream",
		SyncedAt:     time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// upstreamUnchanged 判断上游文件是否与本地一致。优先条件请求（ETag/Last-Modified），
// 退化时比较大小。始终带 Range 且只读极少字节，避免为「检查」而整包下载大文件。
func (c *Cache) upstreamUnchanged(ctx context.Context, artifact Artifact, existing os.FileInfo, prev *Entry) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Range", "bytes=0-0")
	if prev != nil {
		// ETag / Last-Modified 必须原样回传（含引号与 W/ 前缀）。
		if prev.ETag != "" {
			req.Header.Set("If-None-Match", prev.ETag)
		}
		if prev.LastModified != "" {
			req.Header.Set("If-Modified-Since", prev.LastModified)
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	// 上游忽略 Range 时最多也只读 1 字节，随后关闭连接即可取消剩余传输。
	_, _ = io.CopyN(io.Discard, resp.Body, 1)

	if resp.StatusCode == http.StatusNotModified {
		return true, nil
	}
	// 上游直接回了完整对象：大小一致即视为未变化。
	if resp.StatusCode == http.StatusOK && resp.ContentLength > 0 && resp.ContentLength == existing.Size() {
		return true, nil
	}
	// 回的是分片：从 Content-Range 里取完整大小再比较。
	if resp.StatusCode == http.StatusPartialContent {
		if total, ok := totalFromContentRange(resp.Header.Get("Content-Range")); ok && total == existing.Size() {
			return true, nil
		}
	}
	// 条件命中但服务端仍回 200/206 的常见实现：ETag 或 Last-Modified 相同即未变化。
	if prev != nil {
		if prev.ETag != "" && resp.Header.Get("ETag") != "" && strings.TrimSpace(resp.Header.Get("ETag")) == prev.ETag {
			return true, nil
		}
		if prev.LastModified != "" && resp.Header.Get("Last-Modified") != "" && strings.TrimSpace(resp.Header.Get("Last-Modified")) == prev.LastModified {
			return true, nil
		}
	}
	return false, nil
}

// totalFromContentRange 解析 "bytes 0-0/750982223" 中的完整长度。
func totalFromContentRange(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	slash := strings.LastIndex(value, "/")
	if slash < 0 {
		return 0, false
	}
	total, err := strconv.ParseInt(strings.TrimSpace(value[slash+1:]), 10, 64)
	if err != nil || total <= 0 {
		return 0, false
	}
	return total, true
}

// hashFile 计算已缓存文件的 SHA-256，用于补齐旧缓存的清单元数据。
func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (c *Cache) cachedEntry(artifact Artifact) *Entry {
	info, err := os.Stat(c.Path(artifact.Filename))
	if err != nil || info.IsDir() || info.Size() == 0 {
		return nil
	}
	entry := &Entry{
		Filename: artifact.Filename,
		Name:     artifact.Name,
		URL:      artifact.URL,
		Size:     info.Size(),
		Source:   "local",
	}
	for _, prev := range c.Manifest() {
		if prev.Filename == artifact.Filename {
			entry.SHA256 = prev.SHA256
			entry.ETag = prev.ETag
			entry.LastModified = prev.LastModified
			entry.SyncedAt = prev.SyncedAt
			break
		}
	}
	return entry
}

func (c *Cache) writeManifest(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Filename < entries[j].Filename })
	payload := manifest{UpdatedAt: time.Now().UTC().Format(time.RFC3339), Entries: entries}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(c.dir, manifestFileName+".tmp")
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, filepath.Join(c.dir, manifestFileName)); err != nil {
		log.Printf("[downloads] write manifest: %v", err)
	}
}

func IsKnownFile(filename string) bool {
	switch strings.TrimSpace(filename) {
	case WindowsInstallerFile, MacOSInstallerFile:
		return true
	default:
		return false
	}
}
