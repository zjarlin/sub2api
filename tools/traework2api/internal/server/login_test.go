package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"traework2api/internal/auth"
	"traework2api/internal/pool"
	"traework2api/internal/upstream"
)

func TestBrowserLoginSavesAndLoadsAccount(t *testing.T) {
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case upstream.EpExchange:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["RefreshToken"] != "refresh-secret" {
				t.Error("wrong refresh token")
			}
			_, _ = w.Write([]byte(`{"Result":{"Token":"access-secret","RefreshToken":"rotated-secret","TokenExpireAt":1999999999}}`))
		case upstream.EpUserInfo:
			if r.Header.Get("X-Cloudide-Token") != "access-secret" {
				t.Error("missing verified access token")
			}
			_, _ = w.Write([]byte(`{"Result":{"UserID":"123","ScreenName":"Tester","EnterpriseID":"e1"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer remote.Close()
	up := upstream.New()
	up.OAuthHost = remote.URL
	dir := t.TempDir()
	p := pool.New("")
	p.Add(&auth.Auth{UID: "123"})
	p.Disable("123", "expired")
	h := NewHandler(Config{APIKey: "adapter-key", AuthDir: dir, Pool: p, Upstream: up})
	request := func(path, key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("X-Login-Owner", "admin:1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("/internal/login/sessions", "", "{}"); w.Code != 401 {
		t.Fatalf("unauth %d", w.Code)
	}
	w := request("/internal/login/sessions", "adapter-key", "{}")
	var s struct {
		ID  string `json:"session_id"`
		URL string `json:"auth_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	loginURL, _ := url.Parse(s.URL)
	callbackPath := "/internal/login/sessions/" + s.ID + "/callback"
	if w := request(callbackPath, "adapter-key", `{"callback_url":"http://evil.test/authorize?refreshToken=secret"}`); w.Code != 400 || calls != 0 {
		t.Fatalf("invalid callback made upstream call: %d %d", w.Code, calls)
	}
	body := `{"callback_url":"http://127.0.0.1:18080/authorize?refreshToken=refresh-secret&userInfo=%7B%22UserID%22%3A%22evil%22%7D"}`
	w = request(callbackPath, "adapter-key", body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"completed"`) || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("complete %d %s", w.Code, w.Body.String())
	}
	loaded, err := auth.LoadDir(dir)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("load: %v %#v", err, loaded)
	}
	a := loaded[0]
	if a.UID != "123" || a.RefreshToken != "rotated-secret" || a.MachineID != loginURL.Query().Get("machine_id") || a.DeviceID != loginURL.Query().Get("device_id") {
		t.Fatal("wrong persisted identity or device")
	}
	info, err := os.Stat(filepath.Join(dir, "trae-123.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions")
	}
	if p.Pick() == nil {
		t.Fatal("login did not revive pool")
	}
	request(callbackPath, "adapter-key", body)
	if calls != 2 {
		t.Fatalf("callback replay exchanged token again: %d", calls)
	}
}

func TestCallbackRefreshTokenVariants(t *testing.T) {
	for _, value := range []string{`{"RefreshToken":"token+value"}`, url.QueryEscape(`{"RefreshToken":"token+value"}`)} {
		token, err := callbackRefreshToken("http://127.0.0.1:18080/authorize?userJwt=" + url.QueryEscape(value))
		if err != nil || token != "token+value" {
			t.Fatalf("%q %v", token, err)
		}
	}
}
