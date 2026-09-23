//go:build !darwin

package server

import (
	"os"
	"runtime"
	"strings"
)

// Linux 容器从 procfs 读取内核版本，其余系统保持原适配器的缺省值。
func osVersion() string {
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			if version := strings.TrimSpace(string(data)); version != "" {
				return version
			}
		}
	}
	return "0.0"
}
