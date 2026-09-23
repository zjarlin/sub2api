package server

import "syscall"

// macOS 通过 sysctl 读取内核版本；平台专用 API 必须在编译时隔离。
func osVersion() string {
	if version, err := syscall.Sysctl("kern.osrelease"); err == nil && version != "" {
		return version
	}
	return "0.0"
}
