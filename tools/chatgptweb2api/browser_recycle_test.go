package main

import (
	"testing"
	"time"
)

func TestRecycleDueLocked(t *testing.T) {
	// 未启动时不回收，避免把正常返回“not running”的路径变成启动动作。
	var idle chatgptBrowser
	if idle.recycleDueLocked() {
		t.Fatal("idle browser must not be recycled")
	}

	b := &chatgptBrowser{started: true, startedAt: time.Now(), maxAsks: 2, maxAge: time.Hour}
	if b.recycleDueLocked() {
		t.Fatal("fresh browser must not be recycled")
	}
	b.asks = 2
	if !b.recycleDueLocked() {
		t.Fatal("browser must be recycled after max asks")
	}

	aged := &chatgptBrowser{started: true, startedAt: time.Now().Add(-2 * time.Hour), maxAsks: 100, maxAge: time.Hour}
	if !aged.recycleDueLocked() {
		t.Fatal("browser must be recycled after max age")
	}
}
