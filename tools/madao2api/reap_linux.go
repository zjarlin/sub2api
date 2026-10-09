//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// terminateProfileProcesses 结束所有命令行包含指定 profile 目录的 Chromium 进程。
// 只匹配自身 profile 路径，避免误杀其他登录会话。用于回收卡住的登录浏览器。
func terminateProfileProcesses(profileDir string) {
	clean := filepath.Clean(profileDir)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		cmdlinePath := filepath.Join("/proc", entry.Name(), "cmdline")
		raw, err := os.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}
		cmdline := strings.ReplaceAll(string(raw), "\x00", " ")
		if !strings.Contains(cmdline, "chromium") || !strings.Contains(cmdline, clean) {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		// 结束整个进程组（Chromium 会 fork 出 zygote/renderer），避免残留子进程。
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
		}
	}
}
