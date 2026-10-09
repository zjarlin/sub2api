//go:build !linux

package main

// terminateProfileProcesses 在非 Linux 平台不做进程回收（生产运行于 Linux 容器）。
func terminateProfileProcesses(string) {}
