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
