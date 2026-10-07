package routes

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/downloads"
	"github.com/Wei-Shaw/sub2api/internal/setup"

	"github.com/gin-gonic/gin"
)

const codexDesktopInstallerURL = "https://get.microsoft.com/installer/download/9PLM9XGG6VKS"
const codexMacOSInstallerURL = "https://persistent.oaistatic.com/codex-app-prod/Codex.dmg"

var codexDownloadClient = &http.Client{Timeout: 20 * time.Minute}
var codexDownloadCache = downloads.NewCache("")

// RegisterCommonRoutes 注册通用路由（健康检查、状态等）
func RegisterCommonRoutes(r *gin.Engine, checkReady func(context.Context) error) {
	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/ready", func(c *gin.Context) {
		if checkReady == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := checkReady(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	// Claude Code 遥测日志（忽略，直接返回200）
	r.POST("/api/event_logging/batch", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Setup status endpoint (always returns needs_setup: false in normal mode)
	// This is used by the frontend to detect when the service has restarted after setup
	r.GET("/setup/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"needs_setup": false,
				"step":        "completed",
			},
		})
	})

	registerCodexDownloadRoutes(r)
}

func registerCodexDownloadRoutes(r *gin.Engine) {
	r.GET("/downloads/codex-setup.ps1", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Disposition", `attachment; filename="codex-setup.ps1"`)
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(codexWindowsSetupScript))
	})

	// Keep the Microsoft Store bootstrap downloadable through the same origin.
	// The installer still uses Microsoft services, so the setup script applies a
	// hard timeout and offers an independent CLI path.
	r.GET("/downloads/ChatGPT-Installer.exe", func(c *gin.Context) {
		serveCodexInstaller(c, downloads.WindowsInstallerFile, codexDesktopInstallerURL, "ChatGPT-Installer.exe")
	})
	r.GET("/downloads/Codex.dmg", func(c *gin.Context) {
		serveCodexInstaller(c, downloads.MacOSInstallerFile, codexMacOSInstallerURL, "Codex.dmg")
	})

	// 暴露缓存同步状态：文档页据此展示「上次同步时间」，用户也能核对安装包是否随官网更新。
	r.GET("/downloads/manifest.json", func(c *gin.Context) {
		entries := codexDownloadCache.Manifest()
		if entries == nil {
			entries = []downloads.Entry{}
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{
			"refreshed_at": latestSyncedAt(entries),
			"interval":     codexDownloadCache.RefreshInterval().String(),
			"entries":      entries,
		})
	})
}

// latestSyncedAt 返回所有条目里最近的同步时间，供前端显示「随官网更新」的时间点。
func latestSyncedAt(entries []downloads.Entry) string {
	latest := ""
	for _, entry := range entries {
		if entry.SyncedAt > latest {
			latest = entry.SyncedAt
		}
	}
	return latest
}

func serveCodexInstaller(c *gin.Context, cacheFilename, upstreamURL, downloadFilename string) {
	if file, info, err := codexDownloadCache.Open(cacheFilename); err == nil {
		defer file.Close()
		c.Header("Cache-Control", "public, max-age=3600")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, downloadFilename))
		c.Header("Content-Type", "application/octet-stream")
		http.ServeContent(c.Writer, c.Request, downloadFilename, info.ModTime(), file)
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		c.String(http.StatusInternalServerError, "create installer request: %v", err)
		return
	}
	resp, err := codexDownloadClient.Do(req)
	if err != nil {
		c.String(http.StatusBadGateway, "download installer: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		c.String(http.StatusBadGateway, "download installer: upstream HTTP %d", resp.StatusCode)
		return
	}
	c.Header("Cache-Control", "public, max-age=900")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, downloadFilename))
	if contentType := strings.TrimSpace(resp.Header.Get("Content-Type")); contentType != "" {
		c.Header("Content-Type", contentType)
	} else {
		c.Header("Content-Type", "application/octet-stream")
	}
	if contentLength := strings.TrimSpace(resp.Header.Get("Content-Length")); contentLength != "" {
		c.Header("Content-Length", contentLength)
	}
	c.Status(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		_ = c.Error(fmt.Errorf("copy installer response: %w", err))
	}
}

func StartCodexDownloadCache(ctx context.Context) {
	codexDownloadCache = downloads.NewCache(downloads.CacheDir(setup.GetDataDir()))
	codexDownloadCache.Start(ctx)
}

//go:embed codex_setup.ps1
var codexWindowsSetupScript string
