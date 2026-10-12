package model3d

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// hi3dStub 模拟 Hi3D 的 token / submit-task / query-task / balance 四个端点。
type hi3dStub struct {
	tokenCalls  atomic.Int32
	submitCalls atomic.Int32
	queryCalls  atomic.Int32

	submitFields map[string]string
	submitFiles  map[string][]byte
	submitAuth   string

	submitStatus int
	submitBody   string
	queryBody    string
	balanceBody  string
	tokenBody    string
}

func (s *hi3dStub) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/input.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	})
	mux.HandleFunc(hi3dTokenPath, func(w http.ResponseWriter, r *http.Request) {
		s.tokenCalls.Add(1)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
			t.Errorf("token auth = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		body := s.tokenBody
		if body == "" {
			body = `{"code":200,"message":"success","data":{"accessToken":"tok-1","tokenType":"Bearer","nonce":"n"}}`
		}
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc(hi3dSubmitPath, func(w http.ResponseWriter, r *http.Request) {
		s.submitCalls.Add(1)
		s.submitAuth = r.Header.Get("Authorization")
		if s.submitStatus >= 400 {
			w.WriteHeader(s.submitStatus)
			if s.submitBody != "" {
				_, _ = w.Write([]byte(s.submitBody))
			}
			return
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("submit content type = %q", r.Header.Get("Content-Type"))
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
		form, err := reader.ReadForm(4 << 20)
		if err != nil {
			t.Fatalf("read submit form: %v", err)
		}
		s.submitFields = map[string]string{}
		for key, values := range form.Value {
			if len(values) > 0 {
				s.submitFields[key] = values[0]
			}
		}
		s.submitFiles = map[string][]byte{}
		for field, files := range form.File {
			file, err := files[0].Open()
			if err != nil {
				t.Fatalf("open file: %v", err)
			}
			data, _ := readAllLimited(file, 1<<20)
			s.submitFiles[field] = data
		}
		body := s.submitBody
		if body == "" {
			body = `{"code":200,"data":{"task_id":"h1"},"msg":"success"}`
		}
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc(hi3dQueryPath, func(w http.ResponseWriter, r *http.Request) {
		s.queryCalls.Add(1)
		if r.URL.Query().Get("task_id") != "h1" {
			t.Errorf("query task_id = %q", r.URL.Query().Get("task_id"))
		}
		body := s.queryBody
		if body == "" {
			body = `{"code":200,"data":{"task_id":"h1","state":"success","id":"h1_0","url":"https://cdn/0.glb","cover_url":"https://cdn/0.webp"},"msg":"success"}`
		}
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc(hi3dBalancePath, func(w http.ResponseWriter, r *http.Request) {
		body := s.balanceBody
		if body == "" {
			body = `{"code":200,"msg":"success","data":{"totalBalance":14}}`
		}
		_, _ = w.Write([]byte(body))
	})
	return mux
}

func newHi3DForTest(stub *hi3dStub) *Hi3DProvider {
	return NewHi3DProvider(&Hi3DConfig{ClientID: "cid", ClientSecret: "secret", BaseURL: ""}, 5)
}

func TestHi3DSubmitAndQueryReuseToken(t *testing.T) {
	stub := &hi3dStub{}
	server := httptest.NewServer(stub.handler(t))
	defer server.Close()
	provider := NewHi3DProvider(&Hi3DConfig{ClientID: "cid", ClientSecret: "secret", BaseURL: server.URL}, 5)

	taskID, err := provider.Create(context.Background(), &CreateRequest{
		Mode:     ModeImageTo3D,
		ImageURL: "data:image/png;base64,iVBORw0KGgo=",
		Format:   "glb",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if taskID != "h1" {
		t.Fatalf("task id = %q", taskID)
	}
	if stub.submitAuth != "Bearer tok-1" {
		t.Fatalf("submit auth = %q", stub.submitAuth)
	}
	if stub.submitFields["request_type"] != "3" || stub.submitFields["model"] != "hi3dv3.0" {
		t.Fatalf("fields = %+v", stub.submitFields)
	}
	if stub.submitFields["resolution"] != "2048quality" || stub.submitFields["format"] != "2" {
		t.Fatalf("fields = %+v", stub.submitFields)
	}
	if string(stub.submitFiles["images"]) != string([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		t.Fatalf("uploaded image = %v", stub.submitFiles["images"])
	}

	task, err := provider.Get(context.Background(), "h1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.Status != StatusSucceeded || task.Progress != 100 {
		t.Fatalf("task = %+v", task)
	}
	if len(task.Assets) != 1 || task.Assets[0].Format != "glb" || task.Assets[0].URL != "https://cdn/0.glb" {
		t.Fatalf("assets = %+v", task.Assets)
	}
	if task.ThumbnailURL != "https://cdn/0.webp" {
		t.Fatalf("thumbnail = %q", task.ThumbnailURL)
	}
	if stub.tokenCalls.Load() != 1 {
		t.Fatalf("token calls = %d, want 1 (cached)", stub.tokenCalls.Load())
	}
}

func TestHi3DRefreshesTokenOnceOnUnauthorized(t *testing.T) {
	stub := &hi3dStub{submitStatus: http.StatusUnauthorized, submitBody: `{"code":401,"msg":"Unauthorized"}`}
	server := httptest.NewServer(stub.handler(t))
	defer server.Close()
	provider := NewHi3DProvider(&Hi3DConfig{ClientID: "cid", ClientSecret: "secret", BaseURL: server.URL}, 5)

	_, err := provider.Create(context.Background(), &CreateRequest{Mode: ModeImageTo3D, ImageURL: "data:image/png;base64,iVBORw0KGgo="})
	if err == nil {
		t.Fatal("expected 401 to surface")
	}
	upstream, ok := err.(*UpstreamError)
	if !ok || upstream.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v", err)
	}
	if stub.tokenCalls.Load() != 2 {
		t.Fatalf("token calls = %d, want 2 (refresh after 401)", stub.tokenCalls.Load())
	}
	if stub.submitCalls.Load() != 2 {
		t.Fatalf("submit calls = %d, want 2", stub.submitCalls.Load())
	}
}

func TestHi3DTextModeIsUnsupported(t *testing.T) {
	stub := &hi3dStub{}
	server := httptest.NewServer(stub.handler(t))
	defer server.Close()
	provider := NewHi3DProvider(&Hi3DConfig{ClientID: "cid", ClientSecret: "secret", BaseURL: server.URL}, 5)
	if _, err := provider.Create(context.Background(), &CreateRequest{Mode: ModeTextTo3D, Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "only accepts image-to-3d") {
		t.Fatalf("err = %v", err)
	}
	if stub.submitCalls.Load() != 0 {
		t.Fatal("unsupported mode must not call upstream")
	}
}

func TestHi3DFailureStateAndBalance(t *testing.T) {
	stub := &hi3dStub{queryBody: `{"code":200,"data":{"task_id":"h1","state":"failed"},"msg":"generate failed"}`}
	server := httptest.NewServer(stub.handler(t))
	defer server.Close()
	provider := NewHi3DProvider(&Hi3DConfig{ClientID: "cid", ClientSecret: "secret", BaseURL: server.URL}, 5)
	task, err := provider.Get(context.Background(), "h1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if task.Status != StatusFailed || task.Error == "" {
		t.Fatalf("task = %+v", task)
	}
	balance, err := provider.Balance(context.Background())
	if err != nil || balance != 14 {
		t.Fatalf("balance = %v err = %v", balance, err)
	}
}

func TestHi3DBalanceErrorSurfacesProviderCode(t *testing.T) {
	stub := &hi3dStub{balanceBody: `{"code":30010000,"data":{},"msg":"balance is not enough"}`}
	server := httptest.NewServer(stub.handler(t))
	defer server.Close()
	provider := NewHi3DProvider(&Hi3DConfig{ClientID: "cid", ClientSecret: "secret", BaseURL: server.URL}, 5)
	_, err := provider.Balance(context.Background())
	upstream, ok := err.(*UpstreamError)
	if !ok || upstream.Code != "30010000" || upstream.Message != "balance is not enough" {
		t.Fatalf("err = %v", err)
	}
}

func TestHi3DMultiViewUsesMultiImagesAndBit(t *testing.T) {
	stub := &hi3dStub{}
	server := httptest.NewServer(stub.handler(t))
	defer server.Close()
	provider := NewHi3DProvider(&Hi3DConfig{ClientID: "cid", ClientSecret: "secret", BaseURL: server.URL}, 5)
	_, err := provider.Create(context.Background(), &CreateRequest{
		Mode:      ModeImageTo3D,
		ImageURLs: []string{"data:image/png;base64,iVBORw0KGgo=", "data:image/png;base64,iVBORw0KGgo="},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if stub.submitFields["multi_images_bit"] != "1100" {
		t.Fatalf("multi_images_bit = %q", stub.submitFields["multi_images_bit"])
	}
}

func TestHi3DCancelIsUnsupported(t *testing.T) {
	provider := NewHi3DProvider(&Hi3DConfig{ClientID: "cid", ClientSecret: "secret"}, 5)
	if err := provider.Cancel(context.Background(), "h1"); err == nil || !strings.Contains(err.Error(), "does not expose a cancel endpoint") {
		t.Fatalf("err = %v", err)
	}
}

func TestHi3DRequiresCredentials(t *testing.T) {
	provider := NewHi3DProvider(&Hi3DConfig{ClientID: "cid"}, 5)
	if provider.Info().Configured {
		t.Fatal("missing secret must report unconfigured")
	}
}

func TestJWTExpiryParsesExpClaim(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))
	payload, _ := json.Marshal(map[string]any{"exp": time.Now().Add(10 * time.Minute).Unix()})
	token := header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	expiry, ok := jwtExpiry(token)
	if !ok {
		t.Fatal("expected exp claim to parse")
	}
	if time.Until(expiry) > 10*time.Minute || time.Until(expiry) < 8*time.Minute {
		t.Fatalf("expiry = %v", expiry)
	}
	if _, ok := jwtExpiry("not-a-jwt"); ok {
		t.Fatal("non JWT must not parse")
	}
}
