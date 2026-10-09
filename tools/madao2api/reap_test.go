package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 卡住的登录会话（active=true 但实际无可用浏览器）必须能被 Start 自动回收。
func TestReapOrphansAndForceReset(t *testing.T) {
	root := t.TempDir()
	// 先造残留 profile，模拟上次崩溃留下的目录。
	for _, name := range []string{"session-a", "session-b"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// 构造期应清理残留。
	b := newChromiumLoginBrowser("/bin/true", root)
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("constructor should reap stale profiles, got %d", len(entries))
	}
	// 再放残留并模拟 active=true，forceReset 应清空。
	if err := os.MkdirAll(filepath.Join(root, "session-c"), 0o700); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.active = true
	b.forceResetLocked()
	b.mu.Unlock()
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("forceReset should clear profiles, got %d", len(entries))
	}
}
