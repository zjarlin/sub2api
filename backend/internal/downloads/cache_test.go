package downloads

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func partFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*"+partPrefix+"*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return matches
}

func TestRefreshWritesAtomicallyAndServesCachedFile(t *testing.T) {
	content := make([]byte, 128*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "131072")
		_, _ = w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()
	cache := NewCache(dir)
	cache.client = server.Client()
	entry, err := cache.refreshArtifact(context.Background(), Artifact{
		Name: "test", Filename: "test.bin", URL: server.URL, MinBytes: 1,
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if entry.Size != int64(len(content)) || entry.SHA256 == "" || entry.Source != "upstream" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	file, info, err := cache.Open("test.bin")
	if err != nil {
		t.Fatalf("open cached file: %v", err)
	}
	defer file.Close()
	if info.Size() != int64(len(content)) {
		t.Fatalf("size=%d want=%d", info.Size(), len(content))
	}
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read cached file: %v", err)
	}
	if string(got) != string(content) {
		t.Fatal("cached content mismatch")
	}
	if parts := partFiles(t, dir); len(parts) != 0 {
		t.Fatalf("partial files should not remain: %v", parts)
	}
}

func TestRefreshKeepsExistingFileWhenUpstreamFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.bin")
	if err := os.WriteFile(target, make([]byte, 128*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer server.Close()
	cache := NewCache(dir)
	cache.client = server.Client()
	if _, err := cache.refreshArtifact(context.Background(), Artifact{Name: "test", Filename: "test.bin", URL: server.URL, MinBytes: 1}); err == nil {
		t.Fatal("expected refresh error")
	}
	info, statErr := os.Stat(target)
	if statErr != nil || info.Size() != 128*1024 {
		t.Fatalf("existing cache should survive: info=%v err=%v", info, statErr)
	}
}

func TestRefreshSkipsDownloadWhenUpstreamNotModified(t *testing.T) {
	content := make([]byte, 128*1024)
	var downloads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		downloads++
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Content-Length", "131072")
		_, _ = w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()
	cache := NewCache(dir)
	cache.client = server.Client()
	artifact := Artifact{Name: "test", Filename: "test.bin", URL: server.URL, MinBytes: 1}

	first, err := cache.refreshArtifact(context.Background(), artifact)
	if err != nil || first.ETag != `"abc"` {
		t.Fatalf("first refresh: entry=%+v err=%v", first, err)
	}
	// 把 manifest 落盘，模拟上一次刷新记录。
	cache.writeManifest([]Entry{*first})

	second, err := cache.refreshArtifact(context.Background(), artifact)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if second.Source != "upstream-not-modified" {
		t.Fatalf("expected not-modified, got %+v", second)
	}
	if second.SyncedAt == "" {
		t.Fatalf("unchanged entry should still record a sync time: %+v", second)
	}
	if second.SHA256 == "" {
		t.Fatalf("unchanged entry should backfill a hash: %+v", second)
	}
	if downloads != 1 {
		t.Fatalf("expected a single download, got %d", downloads)
	}
}

func TestCleanOrphanPartsRemovesInterruptedDownloads(t *testing.T) {
	dir := t.TempDir()
	// 刷新锁已持有，任何残留分片都视为中断遗留。
	orphan := filepath.Join(dir, "."+MacOSInstallerFile+partPrefix+"123")
	if err := os.WriteFile(orphan, make([]byte, 16), 0o600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, MacOSInstallerFile)
	if err := os.WriteFile(keep, make([]byte, 16), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := NewCache(dir)
	cache.cleanOrphanParts()

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphaned partial should be removed: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("cached artifact must survive cleanup: %v", err)
	}
}

func TestRefreshWritesManifest(t *testing.T) {
	content := make([]byte, 128*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "131072")
		w.Header().Set("Last-Modified", "Tue, 06 Oct 2026 01:41:38 GMT")
		_, _ = w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()
	cache := NewCache(dir)
	cache.client = server.Client()
	// 指向单一测试工件，避免真实网络。
	cache.artifacts = []Artifact{{Name: "test", Filename: "test.bin", URL: server.URL, MinBytes: 1}}

	cache.Refresh(context.Background())

	entries := cache.Manifest()
	if len(entries) != 1 || entries[0].Filename != "test.bin" {
		t.Fatalf("manifest entries: %+v", entries)
	}
	if entries[0].SHA256 == "" || entries[0].LastModified == "" {
		t.Fatalf("manifest entry missing metadata: %+v", entries[0])
	}
	if cache.ManifestUpdatedAt() == "" {
		t.Fatal("manifest updated_at must be recorded")
	}
}

func TestRefreshLockSkipsConcurrentInstance(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	release, ok := cache.acquireLock()
	if !ok {
		t.Fatal("first lock should succeed")
	}
	defer release()

	other := NewCache(dir)
	if _, got := other.acquireLock(); got {
		t.Fatal("second instance must not acquire the refresh lock")
	}
}

func TestKnownFileNames(t *testing.T) {
	if !IsKnownFile(WindowsInstallerFile) || !IsKnownFile(MacOSInstallerFile) || IsKnownFile("../x") {
		t.Fatal("unexpected known-file result")
	}
}

func TestStaticCacheDoesNotStartRefresh(t *testing.T) {
	dir := t.TempDir()
	cache := NewStaticCache(dir)
	if cache.refreshable {
		t.Fatal("static cache must not be refreshable")
	}
	// Start 对静态缓存应是空操作，不写任何文件。
	cache.Start(context.Background())
	if _, err := os.Stat(filepath.Join(dir, manifestFileName)); !os.IsNotExist(err) {
		t.Fatalf("static cache must not write a manifest: %v", err)
	}
}

func TestLoopCanStop(t *testing.T) {
	cache := NewCache(t.TempDir())
	cache.refresh = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cache.Start(ctx)
	cancel()
	time.Sleep(10 * time.Millisecond)
}
