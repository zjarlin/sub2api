package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type refreshTestBrowser struct {
	starts atomic.Int32
	start  func(context.Context, browserLoginOptions) (browserLoginSession, error)
}

func (b *refreshTestBrowser) Start(ctx context.Context, options browserLoginOptions) (browserLoginSession, error) {
	b.starts.Add(1)
	return b.start(ctx, options)
}

type refreshTestSession struct {
	closed     atomic.Bool
	credential func(context.Context) (webCredential, bool, error)
}

func (s *refreshTestSession) Credential(ctx context.Context) (webCredential, bool, error) {
	return s.credential(ctx)
}

func (s *refreshTestSession) Screenshot(context.Context) ([]byte, error) {
	return nil, errors.New("automatic login must not capture screenshots")
}

func (s *refreshTestSession) Close() { s.closed.Store(true) }

type refreshTestPow struct{}

func (refreshTestPow) solveChallenge(context.Context, challenge) (string, error) {
	return "proof", nil
}

func newRefreshTestAdapter(t *testing.T, override http.HandlerFunc) (*adapter, *refreshTestBrowser, *refreshTestSession) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if override != nil {
			recorder := httptest.NewRecorder()
			override(recorder, r)
			if recorder.Body.Len() > 0 || recorder.Code != http.StatusOK {
				for key, values := range recorder.Header() {
					w.Header()[key] = values
				}
				w.WriteHeader(recorder.Code)
				_, _ = w.Write(recorder.Body.Bytes())
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		var data any
		switch r.URL.Path {
		case "/users/current":
			data = map[string]any{"id": "user-1", "email": "user@example.com", "chat": map[string]any{"is_muted": 0}}
		case "/users/auth_token/check_device":
			data = map[string]any{"rotate": map[string]string{"token": "rotated-token"}}
		case "/chat_session/create":
			data = map[string]any{"chat_session": map[string]string{"id": "session-1"}}
		case "/chat/create_pow_challenge":
			data = map[string]any{"challenge": map[string]any{"target_path": completionPath}}
		case "/chat/completion":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"v\":{\"response\":{\"fragments\":[{\"type\":\"RESPONSE\",\"content\":\"renewed reply\"}],\"status\":\"FINISHED\",\"accumulated_token_usage\":3}}}\n\n")
			return
		case "/chat_session/delete":
		default:
			t.Errorf("unexpected upstream route %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(testEnvelope(data))
	}))
	t.Cleanup(upstream.Close)
	a, err := newAdapter("adapter-key", filepath.Join(t.TempDir(), "accounts.json"), &upstreamClient{http: upstream.Client(), baseURL: upstream.URL}, refreshTestPow{})
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := a.encryptLogin("user-1", browserLoginOptions{Email: "user@example.com", Password: "never-echo-this-password"})
	if err != nil {
		t.Fatal(err)
	}
	a.accounts = []webCredential{{UID: "user-1", Email: "user@example.com", Token: "stale-token", DeviceID: "old-device", ReloginCredentials: encrypted}}
	if err := a.save(a.accounts); err != nil {
		t.Fatal(err)
	}
	session := &refreshTestSession{credential: func(context.Context) (webCredential, bool, error) {
		return webCredential{Token: "browser-token", DeviceID: "new-device"}, true, nil
	}}
	browser := &refreshTestBrowser{start: func(_ context.Context, options browserLoginOptions) (browserLoginSession, error) {
		if options.Email != "user@example.com" || options.Password != "never-echo-this-password" {
			t.Error("saved login options were not restored")
		}
		return session, nil
	}}
	a.loginBrowser = browser
	return a, browser, session
}

func waitForRefresh(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("timed out waiting for credential refresh")
		}
	}
}

func TestSavedLoginEncryptionAndOptIn(t *testing.T) {
	a, _, _ := newRefreshTestAdapter(t, nil)
	options := browserLoginOptions{Email: "user@example.com", Password: "never-echo-this-password", AutoRelogin: true}
	imported := webCredential{Token: "browser-token", DeviceID: "new-device"}
	account, err := a.importCredential(context.Background(), imported, options)
	if err != nil || account.AutoRelogin == nil || !*account.AutoRelogin {
		t.Fatalf("enable automatic sign-in: %v", err)
	}
	data, err := os.ReadFile(a.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), options.Password) || strings.Contains(string(data), `"password"`) {
		t.Fatal("persisted login contains a plaintext password")
	}
	info, err := os.Stat(a.stateFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential file must be private")
	}
	response, _ := json.Marshal(account)
	if strings.Contains(string(response), "password") || strings.Contains(string(response), "token") || strings.Contains(string(response), "relogin_credentials") {
		t.Fatal("public login response exposed credentials")
	}
	loaded, err := newAdapter(a.key, a.stateFile, a.upstream, a.pow)
	if err != nil {
		t.Fatal(err)
	}
	credential, _ := loaded.currentCredential("user-1")
	restored, err := loaded.decryptLogin(credential)
	if err != nil || restored != options || credential.Token != "rotated-token" || credential.DeviceID != "new-device" {
		t.Fatalf("saved login could not be restored: %v", err)
	}
	tampered := credential
	tampered.UID = "another-user"
	if _, err := loaded.decryptLogin(tampered); err == nil {
		t.Fatal("encrypted login was usable for another UID")
	}
	loaded.key = "another-adapter-key"
	if _, err := loaded.decryptLogin(credential); err == nil {
		t.Fatal("encrypted login was usable with another key")
	}
	options.Email = "wrong@example.com"
	if _, err := a.importCredential(context.Background(), imported, options); err == nil {
		t.Fatal("mismatched login identity was accepted")
	}
	current, _ := a.currentCredential("user-1")
	if current.ReloginCredentials != credential.ReloginCredentials {
		t.Fatal("failed login replaced stored password")
	}
	account, err = a.importCredential(context.Background(), imported, browserLoginOptions{})
	if err != nil || account.AutoRelogin == nil || *account.AutoRelogin {
		t.Fatalf("disable automatic sign-in: %v", err)
	}
	data, _ = os.ReadFile(a.stateFile)
	if strings.Contains(string(data), "relogin_credentials") {
		t.Fatal("disabled automatic sign-in retained saved login")
	}
}

func TestAutomaticLoginAcceptsMaskedUpstreamEmail(t *testing.T) {
	const maskedEmail = "use******128@example.com"
	a, _, _ := newRefreshTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/current" {
			_ = json.NewEncoder(w).Encode(testEnvelope(map[string]string{"id": "user-1", "email": maskedEmail}))
		}
	})
	options := browserLoginOptions{Email: "user7437128@example.com", Password: "never-echo-this-password", AutoRelogin: true}
	account, err := a.importCredential(context.Background(), webCredential{Token: "browser-token", DeviceID: "new-device"}, options)
	if err != nil || account.AutoRelogin == nil || !*account.AutoRelogin || account.Nickname != maskedEmail {
		t.Fatalf("masked upstream email prevented verified login: %v", err)
	}
	credential, _ := a.currentCredential("user-1")
	if credential.Email != maskedEmail || credential.Token != "rotated-token" {
		t.Fatal("verified upstream account was not preserved")
	}
	saved, err := a.decryptLogin(credential)
	if err != nil || saved != options {
		t.Fatalf("automatic sign-in did not retain the full login email: %v", err)
	}
	data, err := os.ReadFile(a.stateFile)
	if err != nil || strings.Contains(string(data), options.Email) || strings.Contains(string(data), options.Password) {
		t.Fatal("saved masked account exposed the full login email or password")
	}
}

func TestChatRenewsOnlyOnAuthenticationFailure(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		status    int
		body      string
		wantRenew bool
		disabled  bool
	}{
		{name: "HTTP 401", path: "/chat_session/create", status: 401, wantRenew: true},
		{name: "session invalid token", path: "/chat_session/create", body: `{"code":40003}`, wantRenew: true},
		{name: "challenge invalid token", path: "/chat/create_pow_challenge", body: `{"code":0,"data":{"biz_code":40003}}`, wantRenew: true},
		{name: "completion JSON", path: "/chat/completion", body: " \n{\n\"code\":40003\n}", wantRenew: true},
		{name: "completion SSE", path: "/chat/completion", body: "data: {\"code\":40003}\n\n", wantRenew: true},
		{name: "HTTP 403", path: "/chat_session/create", status: 403},
		{name: "upstream quota", path: "/chat_session/create", body: `{"code":40004}`},
		{name: "incomplete stream", path: "/chat/completion", body: "data: {}\n\n"},
		{name: "not configured", path: "/chat_session/create", body: `{"code":40003}`, disabled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			a, browser, session := newRefreshTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/chat_session/create" {
					attempts.Add(1)
				}
				if r.Header.Get("Authorization") != "Bearer stale-token" || r.URL.Path != tc.path {
					return
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = fmt.Fprint(w, tc.body)
			})
			if tc.disabled {
				a.accounts[0].ReloginCredentials = ""
			}
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"deepseek-web-chat","messages":[{"role":"user","content":"hi"}],"stream":true}`))
			a.chat(recorder, req)
			if !tc.wantRenew {
				if recorder.Code == 200 || browser.starts.Load() != 0 || attempts.Load() != 1 {
					t.Fatalf("non-renewable error was retried: status=%d starts=%d attempts=%d", recorder.Code, browser.starts.Load(), attempts.Load())
				}
				if tc.disabled && !strings.Contains(recorder.Body.String(), "deepseek_login_required") {
					t.Fatal("legacy account did not receive an actionable login error")
				}
				return
			}
			if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "renewed reply") || !strings.Contains(recorder.Body.String(), "[DONE]") {
				t.Fatalf("renewed chat failed: %d %s", recorder.Code, recorder.Body.String())
			}
			if browser.starts.Load() != 1 || attempts.Load() != 2 || !session.closed.Load() {
				t.Fatal("refresh did not retry once and close the browser")
			}
			current, _ := a.currentCredential("user-1")
			if current.Token != "rotated-token" || current.DeviceID != "new-device" {
				t.Fatal("verified rotated token and device were not saved")
			}
		})
	}
}

func TestConcurrentRefreshSharesOneBrowser(t *testing.T) {
	a, browser, session := newRefreshTestAdapter(t, nil)
	stale := a.accounts[0]
	gate := make(chan struct{})
	original := session.credential
	session.credential = func(ctx context.Context) (webCredential, bool, error) {
		select {
		case <-gate:
			return original(ctx)
		case <-ctx.Done():
			return webCredential{}, false, ctx.Err()
		}
	}
	const callers = 12
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			credential, err := a.renewCredential(context.Background(), stale)
			if err != nil || credential.Token != "rotated-token" {
				t.Errorf("shared refresh failed: %v", err)
			}
		}()
	}
	waitForRefresh(t, func() bool {
		a.refreshMu.Lock()
		defer a.refreshMu.Unlock()
		refresh := a.refreshes[stale.UID]
		return refresh != nil && refresh.waiters == callers
	})
	close(gate)
	group.Wait()
	if browser.starts.Load() != 1 || !session.closed.Load() {
		t.Fatal("concurrent expired sessions started more than one login")
	}
	if _, err := a.renewCredential(context.Background(), stale); err != nil || browser.starts.Load() != 1 {
		t.Fatal("late stale request did not reuse renewed credentials")
	}
}

func TestRefreshFailureCooldownPreservesCredentials(t *testing.T) {
	for _, failure := range []string{"wrong identity", "browser error", "verification error", "timeout", "save error", "invalid encrypted login"} {
		t.Run(failure, func(t *testing.T) {
			a, browser, session := newRefreshTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/users/current" {
					return
				}
				switch failure {
				case "wrong identity":
					_ = json.NewEncoder(w).Encode(testEnvelope(map[string]string{"id": "another-user", "email": "user@example.com"}))
				case "verification error":
					_, _ = fmt.Fprint(w, `{"code":40003}`)
				}
			})
			switch failure {
			case "browser error":
				browser.start = func(context.Context, browserLoginOptions) (browserLoginSession, error) {
					return nil, errors.New("never-echo-this-password")
				}
			case "timeout":
				a.refreshTimeout = 20 * time.Millisecond
				session.credential = func(context.Context) (webCredential, bool, error) { return webCredential{}, false, nil }
			case "save error":
				a.stateFile = t.TempDir()
			case "invalid encrypted login":
				a.accounts[0].ReloginCredentials = "corrupt"
			}
			stale := a.accounts[0]
			for range 2 {
				_, err := a.renewCredential(context.Background(), stale)
				var public apiError
				if !errors.As(err, &public) || public.status != 503 || public.code != "deepseek_relogin_failed" || strings.Contains(err.Error(), "never-echo-this-password") {
					t.Fatalf("unsafe or non-actionable refresh failure: %v", err)
				}
			}
			current, _ := a.currentCredential(stale.UID)
			if current != stale || browser.starts.Load() > 1 {
				t.Fatal("failed refresh modified credentials or ignored cooldown")
			}
			if failure != "browser error" && failure != "invalid encrypted login" && !session.closed.Load() {
				t.Fatal("failed refresh did not close browser")
			}
		})
	}
}

func TestRefreshCancellationClosesBrowser(t *testing.T) {
	a, _, session := newRefreshTestAdapter(t, nil)
	stale := a.accounts[0]
	entered := make(chan struct{})
	session.credential = func(ctx context.Context) (webCredential, bool, error) {
		close(entered)
		<-ctx.Done()
		return webCredential{}, false, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := a.renewCredential(ctx, stale); done <- err }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller received %v", err)
	}
	waitForRefresh(t, session.closed.Load)
	waitForRefresh(t, func() bool {
		a.refreshMu.Lock()
		defer a.refreshMu.Unlock()
		return len(a.refreshes) == 0
	})
	current, _ := a.currentCredential(stale.UID)
	if current != stale {
		t.Fatal("canceled refresh modified credentials")
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if len(a.refreshFailures) != 0 {
		t.Fatal("canceled request caused an authentication failure cooldown")
	}
}

func TestRefreshedSessionRejectedOnlyRetriesOnce(t *testing.T) {
	var attempts atomic.Int32
	a, browser, _ := newRefreshTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat_session/create" {
			attempts.Add(1)
			_, _ = fmt.Fprint(w, `{"code":40003}`)
		}
	})
	stale := a.accounts[0]
	_, err := a.chatWithRelogin(context.Background(), stale, "hi", false)
	if err == nil || browser.starts.Load() != 1 || attempts.Load() != 2 {
		t.Fatal("chat did not stop after a single refresh retry")
	}
	current, _ := a.currentCredential(stale.UID)
	_, err = a.chatWithRelogin(context.Background(), current, "hi", false)
	if err == nil || browser.starts.Load() != 1 || attempts.Load() != 3 {
		t.Fatal("invalid refreshed session bypassed failure cooldown")
	}
}

func TestCancelOneWaiterKeepsSharedRefresh(t *testing.T) {
	a, browser, session := newRefreshTestAdapter(t, nil)
	stale := a.accounts[0]
	gate := make(chan struct{})
	original := session.credential
	session.credential = func(ctx context.Context) (webCredential, bool, error) {
		select {
		case <-gate:
			return original(ctx)
		case <-ctx.Done():
			return webCredential{}, false, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { _, err := a.renewCredential(ctx, stale); first <- err }()
	go func() { _, err := a.renewCredential(context.Background(), stale); second <- err }()
	waitForRefresh(t, func() bool {
		a.refreshMu.Lock()
		defer a.refreshMu.Unlock()
		refresh := a.refreshes[stale.UID]
		return refresh != nil && refresh.waiters == 2
	})
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal("first waiter did not cancel")
	}
	if session.closed.Load() {
		t.Fatal("one canceled waiter closed the shared browser")
	}
	close(gate)
	if err := <-second; err != nil {
		t.Fatalf("remaining waiter lost the login: %v", err)
	}
	if browser.starts.Load() != 1 || !session.closed.Load() {
		t.Fatal("shared browser lifecycle was incorrect")
	}
}

func TestRefreshDoesNotOverwriteManualLogin(t *testing.T) {
	a, _, session := newRefreshTestAdapter(t, nil)
	stale := a.accounts[0]
	gate := make(chan struct{})
	entered := make(chan struct{})
	original := session.credential
	session.credential = func(ctx context.Context) (webCredential, bool, error) {
		close(entered)
		select {
		case <-gate:
			return original(ctx)
		case <-ctx.Done():
			return webCredential{}, false, ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { _, err := a.renewCredential(context.Background(), stale); done <- err }()
	<-entered
	_, err := a.importCredential(context.Background(), webCredential{Token: "manual-token", DeviceID: "manual-device"}, browserLoginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	current, _ := a.currentCredential(stale.UID)
	if current.DeviceID != "manual-device" || current.ReloginCredentials != "" {
		t.Fatal("automatic refresh overwrote manual credentials or restored a removed password")
	}
}

func TestFailedManualLoginDoesNotChangeSavedPassword(t *testing.T) {
	a, _, _ := newRefreshTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/current" {
			_, _ = fmt.Fprint(w, `{"code":40003}`)
		}
	})
	stale := a.accounts[0]
	for _, enabled := range []bool{false, true} {
		options := browserLoginOptions{Email: "user@example.com", Password: "different-password", AutoRelogin: enabled}
		_, err := a.importCredential(context.Background(), webCredential{Token: "invalid-token", DeviceID: "manual-device"}, options)
		if err == nil {
			t.Fatal("unverified manual login succeeded")
		}
		current, _ := a.currentCredential(stale.UID)
		if current != stale {
			t.Fatal("failed manual login modified saved credentials")
		}
	}
}

func TestAutomaticLoginRequiresEmailAndPassword(t *testing.T) {
	a, browser, _ := newRefreshTestAdapter(t, nil)
	_, err := a.beginLogin(context.Background(), json.RawMessage(`{"auto_relogin":true}`))
	if err == nil || browser.starts.Load() != 0 {
		t.Fatal("automatic login was enabled without an email and password")
	}
}

func TestNewWaiterWaitsForCanceledBrowserCleanup(t *testing.T) {
	a, browser, oldSession := newRefreshTestAdapter(t, nil)
	stale := a.accounts[0]
	entered := make(chan struct{})
	cleanup := make(chan struct{})
	oldSession.credential = func(ctx context.Context) (webCredential, bool, error) {
		close(entered)
		<-ctx.Done()
		<-cleanup
		return webCredential{}, false, ctx.Err()
	}
	newSession := &refreshTestSession{credential: func(context.Context) (webCredential, bool, error) {
		return webCredential{Token: "browser-token", DeviceID: "new-device"}, true, nil
	}}
	browser.start = func(context.Context, browserLoginOptions) (browserLoginSession, error) {
		if browser.starts.Load() == 1 {
			return oldSession, nil
		}
		if !oldSession.closed.Load() {
			t.Error("new login raced the abandoned browser cleanup")
		}
		return newSession, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := a.renewCredential(ctx, stale); first <- err }()
	<-entered
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal("first waiter did not cancel")
	}
	second := make(chan error, 1)
	go func() { _, err := a.renewCredential(context.Background(), stale); second <- err }()
	select {
	case err := <-second:
		t.Fatalf("new waiter completed before the abandoned browser closed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(cleanup)
	if err := <-second; err != nil {
		t.Fatalf("new waiter inherited a canceled refresh: %v", err)
	}
	if browser.starts.Load() != 2 || !newSession.closed.Load() {
		t.Fatal("new waiter did not complete a fresh login")
	}
}

func TestCancellationDuringVerificationDoesNotStartCooldown(t *testing.T) {
	verifying := make(chan struct{})
	a, _, session := newRefreshTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/current" {
			close(verifying)
			<-r.Context().Done()
			w.WriteHeader(503)
		}
	})
	stale := a.accounts[0]
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := a.renewCredential(ctx, stale); done <- err }()
	<-verifying
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("verification caller did not cancel")
	}
	waitForRefresh(t, func() bool {
		a.refreshMu.Lock()
		defer a.refreshMu.Unlock()
		return len(a.refreshes) == 0
	})
	if !session.closed.Load() {
		t.Fatal("verification cancellation did not close the browser")
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if len(a.refreshFailures) != 0 {
		t.Fatal("verification cancellation created a failure cooldown")
	}
}

func TestAbandonedBrowserSessionExpires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	profile := filepath.Join(t.TempDir(), "profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	var released atomic.Int32
	done := make(chan struct{})
	session := &chromiumLoginSession{
		context: ctx, cancelBrowser: cancel, cancelAllocator: func() {}, profile: profile,
		release: func() { released.Add(1); close(done) },
	}
	session.expireAfter(20 * time.Millisecond)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("abandoned browser did not expire")
	}
	session.Close()
	if released.Load() != 1 || ctx.Err() != context.Canceled {
		t.Fatal("browser expiry failed to cancel and release exactly once")
	}
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("browser expiry retained its credential profile")
	}
}
