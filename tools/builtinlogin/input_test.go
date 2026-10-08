package builtinlogin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func newInputHarness(t *testing.T, received *[]Input, mu *sync.Mutex) (http.Handler, string) {
	t.Helper()
	h := New(func(context.Context) (*Flow, error) {
		return &Flow{
			URL:  "https://example.com/authorize",
			Mode: "poll",
			Complete: func(context.Context, string) (*Account, error) {
				return nil, ErrPending
			},
			Input: func(_ context.Context, in Input) error {
				mu.Lock()
				*received = append(*received, in)
				mu.Unlock()
				return nil
			},
		}, nil
	})
	h.now = time.Now
	mux := http.NewServeMux()
	h.Register(mux, func(next http.HandlerFunc) http.HandlerFunc { return next })
	w := loginRequest(mux, "POST", "/internal/login/sessions", "admin:1", "{}")
	var started result
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	return mux, started.ID
}

func TestLoginInputRelayAndValidation(t *testing.T) {
	var mu sync.Mutex
	var received []Input
	mux, id := newInputHarness(t, &received, &mu)
	path := "/internal/login/sessions/" + id + "/input"

	// 缺少/不匹配 owner 的会话必须不可见（返回 404，不泄漏会话是否存在）。
	if w := loginRequest(mux, "POST", path, "", `{"type":"click","x":1,"y":2}`); w.Code != 404 {
		t.Fatalf("missing owner: %d", w.Code)
	}
	if w := loginRequest(mux, "POST", path, "admin:other", `{"type":"click","x":1,"y":2}`); w.Code != 404 {
		t.Fatalf("foreign owner: %d", w.Code)
	}
	// 合法点击被转发。
	if w := loginRequest(mux, "POST", path, "admin:1", `{"type":"click","x":120,"y":240}`); w.Code != 200 {
		t.Fatalf("valid click: %d %s", w.Code, w.Body.String())
	}
	// 文本输入。
	if w := loginRequest(mux, "POST", path, "admin:1", `{"type":"text","text":"13800138000"}`); w.Code != 200 {
		t.Fatalf("valid text: %d", w.Code)
	}
	// 非法类型与超长文本必须被拒。
	if w := loginRequest(mux, "POST", path, "admin:1", `{"type":"bogus"}`); w.Code != 400 {
		t.Fatalf("invalid type: %d", w.Code)
	}
	tooLong := `{"type":"text","text":"` + strings.Repeat("a", 4097) + `"}`
	if w := loginRequest(mux, "POST", path, "admin:1", tooLong); w.Code != 400 {
		t.Fatalf("overlong text: %d", w.Code)
	}
	// 越界坐标必须被拒。
	if w := loginRequest(mux, "POST", path, "admin:1", `{"type":"click","x":-5,"y":2}`); w.Code != 400 {
		t.Fatalf("negative coord: %d", w.Code)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 || received[0].Type != "click" || received[1].Text != "13800138000" {
		t.Fatalf("unexpected relayed events: %+v", received)
	}
}
