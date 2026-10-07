package translate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestMyMemoryTranslate(t *testing.T) {
	var texts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("langpair") != "en|zh-CN" || q.Get("de") != "owner@example.com" || q.Get("key") != "test-key" {
			t.Errorf("incorrect MyMemory parameters")
		}
		texts = append(texts, q.Get("q"))
		_, _ = w.Write([]byte(`{"responseData":{"translatedText":"你好 &amp; 世界"},"responseStatus":"200","quotaFinished":false}`))
	}))
	defer srv.Close()
	tr := NewMyMemoryTranslator(&MyMemoryConfig{Email: "owner@example.com", APIKey: "test-key"})
	tr.endpoint = srv.URL
	resp, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello & world", "", "Second\nline"}, SourceLang: "en", TargetLang: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(texts, []string{"Hello & world", "Second\nline"}) || resp.Provider != "mymemory" || len(resp.Translations) != 3 || resp.Translations[0].Text != "你好 & 世界" || resp.Translations[1].Text != "" {
		t.Fatalf("unexpected response: %+v, texts=%v", resp, texts)
	}
}

func TestMyMemoryValidationAndErrors(t *testing.T) {
	tr := NewMyMemoryTranslator(nil)
	for _, req := range []*TranslateRequest{nil, {Text: []string{"Hello"}, TargetLang: "zh-CN"}, {Text: []string{strings.Repeat("你", 167)}, SourceLang: "zh-CN", TargetLang: "en"}, {Text: []string{"Hello"}, SourceLang: "en", TargetLang: "zh-CN", Format: "html"}} {
		if _, err := tr.Translate(context.Background(), req); err == nil {
			t.Fatal("expected validation error")
		}
	}
	for _, body := range []string{
		`{"responseData":{"translatedText":"quota error text"},"responseStatus":200,"quotaFinished":true}`,
		`{"responseData":{"translatedText":"invalid language error text"},"responseStatus":"403"}`,
		`{"responseData":{"translatedText":""},"responseStatus":200}`,
		`{"responseData":{"translatedText":"unexpected"}}`,
		`<html>captcha</html>`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		tr.endpoint = srv.URL
		if _, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello"}, SourceLang: "en", TargetLang: "zh-CN"}); err == nil {
			t.Errorf("expected upstream error for %s", body)
		}
		srv.Close()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tr.Translate(ctx, &TranslateRequest{Text: []string{"private text"}, SourceLang: "en", TargetLang: "zh-CN"})
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "private text") {
		t.Fatalf("unsafe cancellation: %v", err)
	}
}

func TestLibreTranslateAndDetect(t *testing.T) {
	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("incorrect LibreTranslate method or content type")
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["api_key"] != "test-key" {
			t.Errorf("missing instance key")
		}
		if r.URL.Path == "/prefix/detect" {
			_, _ = w.Write([]byte(`[{"language":"fr","confidence":0.1},{"language":"en","confidence":0.9}]`))
			return
		}
		if r.URL.Path != "/prefix/translate" || body["source"] != "auto" || body["target"] != "zh" || body["format"] != "text" {
			t.Errorf("incorrect LibreTranslate parameters: %v", body)
		}
		count++
		_, _ = w.Write([]byte(`{"translatedText":"你好世界","detectedLanguage":{"language":"en","confidence":0.9}}`))
	}))
	defer srv.Close()
	tr := NewLibreTranslateTranslator(&LibreTranslateConfig{BaseURL: srv.URL + "/prefix/", APIKey: "test-key"})
	resp, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello", "", "World"}, TargetLang: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || resp.Provider != "libretranslate" || len(resp.Translations) != 3 || resp.Translations[0].DetectedLanguage != "en" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	lang, err := tr.DetectLanguage(context.Background(), "Hello")
	if err != nil || lang != "en" {
		t.Fatalf("detection = %q, %v", lang, err)
	}
}

func TestLibreTranslateFailuresAndLanguageMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{429, `rate limited`}, {503, `unavailable`}, {200, `{"error":"unsupported language"}`}, {200, `{"translatedText":[]}`}, {200, `{"translatedText":""}`}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		tr := NewLibreTranslateTranslator(&LibreTranslateConfig{BaseURL: srv.URL})
		if _, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello"}, TargetLang: "zh-CN"}); err == nil {
			t.Errorf("expected failure: %v", tc)
		}
		srv.Close()
	}
	for _, value := range []string{"", ":invalid", "file:///tmp/test", "https://user:secret@host/", "http://localhost/?key=secret"} {
		tr := NewLibreTranslateTranslator(&LibreTranslateConfig{BaseURL: value})
		_, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello"}, TargetLang: "zh-CN"})
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid instance must fail safely: %v", err)
		}
	}
	for input, want := range map[string]string{"zh-CN": "zh", "zh-TW": "zt", "en-US": "en", "auto": "auto", "": ""} {
		if got := libreLanguage(input); got != want {
			t.Errorf("language(%q)=%q want %q", input, got, want)
		}
	}
}

func TestPublicProvidersSelectionAndFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"translatedText":"你好","detectedLanguage":{"language":"en"}}`))
	}))
	defer srv.Close()
	a := NewAggregator(&Config{MyMemory: &MyMemoryConfig{}, LibreTranslate: &LibreTranslateConfig{BaseURL: srv.URL}})
	resp, err := a.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello"}, TargetLang: "zh-CN"})
	if err != nil || resp.Provider != "libretranslate" {
		t.Fatalf("auto-source fallback failed: %+v, %v", resp, err)
	}
	_, err = a.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello"}, TargetLang: "zh-CN", Provider: "mymemory"})
	if err == nil {
		t.Fatal("explicit provider must not silently fall back")
	}
	_, err = a.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello"}, TargetLang: "zh-CN", Provider: "unknown"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("unknown provider = %v", err)
	}
}

// TestPublicProvidersLive 只在显式要求时访问真实上游。
func TestPublicProvidersLive(t *testing.T) {
	if os.Getenv("TRANSLATE_PUBLIC_LIVE_TEST") != "1" {
		t.Skip("set TRANSLATE_PUBLIC_LIVE_TEST=1 to call upstream")
	}
	a := NewAggregator(&Config{MyMemory: &MyMemoryConfig{}, LibreTranslate: &LibreTranslateConfig{BaseURL: os.Getenv("TRANSLATE_LIBRETRANSLATE_URL")}})
	for _, provider := range []string{"mymemory", "libretranslate"} {
		t.Run(provider, func(t *testing.T) {
			resp, err := a.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello world"}, SourceLang: "en", TargetLang: "zh-CN", Provider: provider})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Provider != provider || len(resp.Translations) != 1 || resp.Translations[0].Text == "" {
				t.Fatalf("unexpected live result: %+v", resp)
			}
			t.Logf("provider=%s translations=%+v", resp.Provider, resp.Translations)
		})
	}
}
