package translate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestGoogleWebTranslate(t *testing.T) {
	var inputs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || q.Get("client") != "gtx" || q.Get("sl") != "auto" || q.Get("tl") != "zh-CN" || q.Get("dt") != "t" || q.Get("dj") != "1" {
			t.Errorf("unexpected request parameters: %v", q)
		}
		inputs = append(inputs, q.Get("q"))
		_ = json.NewEncoder(w).Encode(map[string]any{"sentences": []map[string]string{{"trans": "你好"}, {"trans": "\n世界"}}, "src": "en"})
	}))
	defer srv.Close()
	tr := NewGoogleWebTranslator(nil)
	tr.endpoint = srv.URL
	resp, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello\nworld & ?", "", "Second"}, TargetLang: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inputs, []string{"Hello\nworld & ?", "Second"}) {
		t.Fatalf("inputs = %v", inputs)
	}
	if resp.Provider != "google_web" || len(resp.Translations) != 3 || resp.Translations[0].Text != "你好\n世界" || resp.Translations[1].Text != "" || resp.Translations[2].DetectedLanguage != "en" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestGoogleWebErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"rate limit", 429, `rate limited`},
		{"captcha", 403, `<html>captcha</html>`},
		{"invalid json", 200, `<html>error</html>`},
		{"missing translation", 200, `{"sentences":[],"src":"en"}`},
		{"wrong type", 200, `{"sentences":[{"trans":12}]}`},
		{"empty translation", 200, `{"sentences":[{"trans":""}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			tr := NewGoogleWebTranslator(nil)
			tr.endpoint = srv.URL
			if _, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello"}, TargetLang: "zh-CN"}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestGoogleWebExplicitSourceAndDetection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"sentences":[{"trans":"Hello"}],"src":"zh-CN"}`))
	}))
	defer srv.Close()
	tr := NewGoogleWebTranslator(nil)
	tr.endpoint = srv.URL
	resp, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"你好"}, SourceLang: "zh-CN", TargetLang: "en"})
	if err != nil || resp.Translations[0].DetectedLanguage != "" {
		t.Fatalf("explicit source response = %v, %v", resp, err)
	}
	lang, err := tr.DetectLanguage(context.Background(), "你好")
	if err != nil || lang != "zh-CN" {
		t.Fatalf("detection = %q, %v", lang, err)
	}
	if _, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"<b>Hello</b>"}, TargetLang: "en", Format: "html"}); err == nil {
		t.Fatal("expected unsupported format error")
	}
}

func TestGoogleWebProxyAndCancellation(t *testing.T) {
	tr := NewGoogleWebTranslator(&GoogleWebConfig{ProxyURL: "http://user:secret@%invalid"})
	_, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"private text"}, TargetLang: "en"})
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private text") {
		t.Fatalf("invalid proxy error must be sanitized: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tr = NewGoogleWebTranslator(nil)
	if _, err := tr.Translate(ctx, &TranslateRequest{Text: []string{"Hello"}, TargetLang: "en"}); err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestAggregatorGoogleWebFallback(t *testing.T) {
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer failed.Close()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"sentences":[{"trans":"你好"}],"src":"en"}`))
	}))
	defer ok.Close()
	a := NewAggregator(&Config{EnableFreeProviders: true, GoogleWeb: &GoogleWebConfig{}})
	if !reflect.DeepEqual(a.AvailableProviders(), []string{"caiyun", "google_web"}) {
		t.Fatalf("providers = %v", a.AvailableProviders())
	}
	a.translators[0].(*CaiyunTranslator).endpoint = failed.URL
	a.translators[1].(*GoogleWebTranslator).endpoint = ok.URL
	resp, err := a.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello"}, TargetLang: "zh-CN"})
	if err != nil || resp.Provider != "google_web" {
		t.Fatalf("fallback = %v, %v", resp, err)
	}
}

// TestGoogleWebLive 仅在显式开启时访问上游，不让常规测试依赖外网。
func TestGoogleWebLive(t *testing.T) {
	if os.Getenv("TRANSLATE_GOOGLE_WEB_LIVE_TEST") != "1" {
		t.Skip("set TRANSLATE_GOOGLE_WEB_LIVE_TEST=1 to call upstream")
	}
	tr := NewGoogleWebTranslator(&GoogleWebConfig{ProxyURL: os.Getenv("TRANSLATE_GOOGLE_WEB_PROXY_URL")})
	resp, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello world", "Thank you"}, TargetLang: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Translations) != 2 || resp.Translations[0].Text == "" || resp.Translations[0].DetectedLanguage != "en" {
		t.Fatalf("unexpected live response: %+v", resp)
	}
	t.Logf("provider=%s translations=%+v", resp.Provider, resp.Translations)
}
