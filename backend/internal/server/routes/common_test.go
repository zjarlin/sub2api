package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/downloads"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCommonReadinessChecksDependencies(t *testing.T) {
	for _, tc := range []struct {
		name       string
		checkReady func(context.Context) error
		wantStatus int
	}{
		{name: "ready", checkReady: func(context.Context) error { return nil }, wantStatus: http.StatusOK},
		{name: "dependency_failed", checkReady: func(context.Context) error { return errors.New("database down") }, wantStatus: http.StatusServiceUnavailable},
		{name: "unconfigured", wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			RegisterCommonRoutes(router, tc.checkReady)
			ready := httptest.NewRecorder()
			router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
			require.Equal(t, tc.wantStatus, ready.Code)
			live := httptest.NewRecorder()
			router.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/health", nil))
			require.Equal(t, http.StatusOK, live.Code)
		})
	}
}

func TestCodexSetupScriptRoute(t *testing.T) {
	router := gin.New()
	RegisterCommonRoutes(router, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/downloads/codex-setup.ps1", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Get-AppxPackage")
	require.Contains(t, recorder.Body.String(), "registry.npmmirror.com")
	require.Contains(t, recorder.Header().Get("Content-Disposition"), "codex-setup.ps1")
}

func TestCachedInstallerRoutePrefersLocalFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, downloads.WindowsInstallerFile)
	content := []byte("cached installer")
	require.NoError(t, os.WriteFile(target, content, 0o644))
	codexDownloadCache = downloads.NewStaticCache(dir)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/downloads/ChatGPT-Installer.exe", nil)
	serveCodexInstaller(ctx, downloads.WindowsInstallerFile, "https://example.invalid", "ChatGPT-Installer.exe")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, content, recorder.Body.Bytes())
	require.Equal(t, "application/octet-stream", recorder.Header().Get("Content-Type"))

	rangeRecorder := httptest.NewRecorder()
	rangeCtx, _ := gin.CreateTestContext(rangeRecorder)
	rangeCtx.Request = httptest.NewRequest(http.MethodGet, "/downloads/ChatGPT-Installer.exe", nil)
	rangeCtx.Request.Header.Set("Range", "bytes=0-5")
	serveCodexInstaller(rangeCtx, downloads.WindowsInstallerFile, "https://example.invalid", "ChatGPT-Installer.exe")
	require.Equal(t, http.StatusPartialContent, rangeRecorder.Code)
	require.Equal(t, content[:6], rangeRecorder.Body.Bytes())
}

func TestCodexDownloadManifestRoute(t *testing.T) {
	dir := t.TempDir()
	codexDownloadCache = downloads.NewStaticCache(dir)

	router := gin.New()
	RegisterCommonRoutes(router, nil)

	// 没有 manifest 时仍是合法响应：空条目 + 刷新周期说明。
	empty := httptest.NewRecorder()
	router.ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/downloads/manifest.json", nil))
	require.Equal(t, http.StatusOK, empty.Code)
	require.Contains(t, empty.Body.String(), `"entries":[]`)
	require.Contains(t, empty.Body.String(), `"interval"`)

	// 落盘 manifest 后应能读出同步时间与包信息。
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{
  "updated_at": "2026-10-07T02:18:05Z",
  "entries": [{"filename":"Codex.dmg","name":"macOS Codex installer","url":"https://example.invalid","size":750982223,"sha256":"deadbeef","source":"upstream","synced_at":"2026-10-07T02:18:05Z"}]
}`), 0o644))

	full := httptest.NewRecorder()
	router.ServeHTTP(full, httptest.NewRequest(http.MethodGet, "/downloads/manifest.json", nil))
	require.Equal(t, http.StatusOK, full.Code)
	require.Contains(t, full.Body.String(), `"refreshed_at":"2026-10-07T02:18:05Z"`)
	require.Contains(t, full.Body.String(), `"Codex.dmg"`)
	require.Contains(t, full.Body.String(), `"sha256":"deadbeef"`)
}
