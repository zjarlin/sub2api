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

func TestRefreshWritesAtomicallyAndServesCachedFile(t *testing.T) {
	content := make([]byte, 128*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "131072")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Length", "131072")
		_, _ = w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()
	cache := NewCache(dir)
	cache.client = server.Client()
	cache.refreshArtifact(context.Background(), Artifact{
		Name: "test", Filename: "test.bin", URL: server.URL, MinBytes: 1,
	})
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
	if _, err := os.Stat(filepath.Join(dir, "test.bin.part")); !os.IsNotExist(err) {
		t.Fatalf("partial file should not remain: %v", err)
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
	err := cache.refreshArtifact(context.Background(), Artifact{Name: "test", Filename: "test.bin", URL: server.URL, MinBytes: 1})
	if err == nil {
		t.Fatal("expected refresh error")
	}
	info, statErr := os.Stat(target)
	if statErr != nil || info.Size() != 128*1024 {
		t.Fatalf("existing cache should survive: info=%v err=%v", info, statErr)
	}
}

func TestKnownFileNames(t *testing.T) {
	if !IsKnownFile(WindowsInstallerFile) || !IsKnownFile(MacOSInstallerFile) || IsKnownFile("../x") {
		t.Fatal("unexpected known-file result")
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
