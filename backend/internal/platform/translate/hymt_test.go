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

func TestHyMTTranslate(t *testing.T) {
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/prefix/v1/chat/completions" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("incorrect request method, endpoint or headers")
		}
		var body struct {
			Model         string                           `json:"model"`
			Messages      []struct{ Role, Content string } `json:"messages"`
			Stream        bool                             `json:"stream"`
			MaxTokens     int                              `json:"max_tokens"`
			Temperature   float64                          `json:"temperature"`
			TopP          float64                          `json:"top_p"`
			TopK          int                              `json:"top_k"`
			RepeatPenalty float64                          `json:"repeat_penalty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Model != "hy-mt2" || body.Stream || body.MaxTokens != 2048 || body.Temperature != 0.7 || body.TopP != 0.6 || body.TopK != 20 || body.RepeatPenalty != 1.05 || len(body.Messages) != 1 || body.Messages[0].Role != "user" {
			t.Errorf("incorrect model payload: %+v", body)
			return
		}
		prompts = append(prompts, body.Messages[0].Content)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":" translated "},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	tr := NewHyMTTranslator(&HyMTConfig{BaseURL: srv.URL + "/prefix/", APIKey: "test-key"})
	resp, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"Hello", " \n", "second\nline"}, TargetLang: "zh-TW"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Provider != "hymt" || len(resp.Translations) != 3 || resp.Translations[0].Text != "translated" || resp.Translations[1].Text != " \n" || resp.Translations[0].DetectedLanguage != "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	want := []string{"Translate the following text into Traditional Chinese. Note that you should only output the translated result without any additional explanation:\n\nHello", "Translate the following text into Traditional Chinese. Note that you should only output the translated result without any additional explanation:\n\nsecond\nline"}
	if !reflect.DeepEqual(prompts, want) {
		t.Fatalf("prompts = %v", prompts)
	}
	if _, err := tr.DetectLanguage(context.Background(), "Hello"); err == nil {
		t.Fatal("must not fabricate detected language")
	}
}

func TestHyMTValidation(t *testing.T) {
	tr := NewHyMTTranslator(nil)
	for _, req := range []*TranslateRequest{
		nil, {}, {Text: []string{"hello"}},
		{Text: []string{"hello"}, TargetLang: "auto"},
		{Text: []string{"hello"}, TargetLang: "en", SourceLang: "not-a-language"},
		{Text: []string{"hello"}, TargetLang: "en", Format: "html"},
		{Text: []string{"hello", strings.Repeat("x", 4097)}, TargetLang: "en"},
		{Text: make([]string, 17), TargetLang: "en"},
	} {
		if _, err := tr.Translate(context.Background(), req); err == nil {
			t.Fatalf("expected validation error: %+v", req)
		}
	}
	for code, want := range map[string]string{"zh-CN": "Chinese", "zh-Hans": "Chinese", "zh-Hant-TW": "Traditional Chinese", "zh-HK": "Traditional Chinese", "en-US": "English", "fil": "Filipino", "ja": "Japanese"} {
		if got, ok := hyMTLanguage(code); !ok || got != want {
			t.Fatalf("language %q = %q", code, got)
		}
	}
	for _, endpoint := range []string{"", "file:///tmp/model", "http://user:secret@host/", "http://host/?key=secret", ":invalid", "http://host/#fragment"} {
		tr := NewHyMTTranslator(&HyMTConfig{BaseURL: endpoint})
		if _, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"private text"}, TargetLang: "en"}); err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private text") {
			t.Fatalf("unsafe URL error: %v", err)
		}
	}
}

func TestHyMTErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{503, "private text secret"}, {429, "busy"}, {200, "<html>bad</html>"},
		{200, `{"error":{"message":"failed"}}`}, {200, `{"choices":[]}`},
		{200, `{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`},
		{200, `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`},
		{200, `{"choices":[{"message":{"content":"partial"}}]}`},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		tr := NewHyMTTranslator(&HyMTConfig{BaseURL: srv.URL})
		_, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"private text"}, TargetLang: "en"})
		if err == nil || strings.Contains(err.Error(), "private text") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe upstream error: %v", err)
		}
		srv.Close()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) }))
	defer srv.Close()
	tr := NewHyMTTranslator(&HyMTConfig{BaseURL: srv.URL})
	_, err := tr.Translate(context.Background(), &TranslateRequest{Text: []string{"hello"}, TargetLang: "en"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("redirect followed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = tr.Translate(ctx, &TranslateRequest{Text: []string{"hello"}, TargetLang: "en"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestHyMTExplicitProviderDoesNotFallBack(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/v1/chat/completions" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"translatedText":"fallback"}`))
	}))
	defer srv.Close()
	a := NewAggregator(&Config{LibreTranslate: &LibreTranslateConfig{BaseURL: srv.URL}, HyMT: &HyMTConfig{BaseURL: srv.URL}})
	_, err := a.Translate(context.Background(), &TranslateRequest{Text: []string{"hello"}, TargetLang: "en", Provider: "hymt"})
	if err == nil || calls != 1 {
		t.Fatalf("explicit local request must not fall back: calls=%d, err=%v", calls, err)
	}
}

func TestHyMTLive(t *testing.T) {
	if os.Getenv("TRANSLATE_HYMT_LIVE_TEST") != "1" {
		t.Skip("set TRANSLATE_HYMT_LIVE_TEST=1 for local model acceptance")
	}
	tr := NewHyMTTranslator(&HyMTConfig{BaseURL: os.Getenv("TRANSLATE_HYMT_URL"), APIKey: os.Getenv("TRANSLATE_HYMT_API_KEY")})
	for _, req := range []*TranslateRequest{
		{Text: []string{"Hello world", "The device is offline. Check the network connection and try again."}, TargetLang: "zh-CN"},
		{Text: []string{"修改配置后，请重启服务使其生效。"}, SourceLang: "zh-CN", TargetLang: "en"},
	} {
		resp, err := tr.Translate(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Provider != "hymt" || len(resp.Translations) != len(req.Text) {
			t.Fatalf("unexpected response: %+v", resp)
		}
		for i, item := range resp.Translations {
			if strings.TrimSpace(item.Text) == "" || item.Text == req.Text[i] {
				t.Fatalf("missing translation: %+v", item)
			}
			t.Logf("%s => %s", req.Text[i], item.Text)
		}
	}
}
