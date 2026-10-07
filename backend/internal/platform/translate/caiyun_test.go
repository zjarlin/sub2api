package translate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCaiyunTransType 覆盖 ISO 代码到彩云语向的映射与不支持语言的报错。
func TestCaiyunTransType(t *testing.T) {
	cases := []struct {
		source, target, want string
		wantErr              bool
	}{
		{"en", "zh-CN", "en2zh", false},
		{"", "zh-CN", "auto2zh", false},
		{"zh-CN", "en", "zh2en", false},
		{"ja", "zh-CN", "ja2zh", false},
		{"en", "fr", "", true},
	}
	for _, c := range cases {
		got, err := caiyunTransType(c.source, c.target)
		if c.wantErr {
			if err == nil {
				t.Fatalf("caiyunTransType(%q,%q) expected error", c.source, c.target)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("caiyunTransType(%q,%q)=%q,%v want %q", c.source, c.target, got, err, c.want)
		}
	}
}

// TestCaiyunTranslate 校验请求体、鉴权头与响应解析。
func TestCaiyunTranslate(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("x-authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rc":         0,
			"trans_type": "en2zh",
			"target":     []string{"你好，世界"},
		})
	}))
	defer srv.Close()

	tr := NewCaiyunTranslator(nil)
	tr.endpoint = srv.URL

	resp, err := tr.Translate(context.Background(), &TranslateRequest{
		Text: []string{"hello world"}, SourceLang: "en", TargetLang: "zh-CN",
	})
	if err != nil {
		t.Fatalf("Translate error: %v", err)
	}
	if gotAuth != caiyunDefaultToken {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotBody["trans_type"] != "en2zh" {
		t.Fatalf("trans_type = %v", gotBody["trans_type"])
	}
	if resp.Provider != "caiyun" || len(resp.Translations) != 1 || resp.Translations[0].Text != "你好，世界" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

// TestCaiyunTranslateAutoDetect 校验 source 为空时回填检测到的源语言。
func TestCaiyunTranslateAutoDetect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rc": 0, "trans_type": "zh2en", "target": []string{"Hello world"},
		})
	}))
	defer srv.Close()

	tr := NewCaiyunTranslator(nil)
	tr.endpoint = srv.URL

	resp, err := tr.Translate(context.Background(), &TranslateRequest{
		Text: []string{"你好世界"}, TargetLang: "en",
	})
	if err != nil {
		t.Fatalf("Translate error: %v", err)
	}
	if resp.Translations[0].DetectedLanguage != "zh" {
		t.Fatalf("detected = %q", resp.Translations[0].DetectedLanguage)
	}
}

// TestCaiyunTranslateAPIError 校验 rc 非 0 时返回错误。
func TestCaiyunTranslateAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"rc": 1001, "message": "rate limited"})
	}))
	defer srv.Close()

	tr := NewCaiyunTranslator(nil)
	tr.endpoint = srv.URL

	if _, err := tr.Translate(context.Background(), &TranslateRequest{
		Text: []string{"hi"}, TargetLang: "zh-CN",
	}); err == nil {
		t.Fatal("expected api error")
	}
}

// TestAggregatorFreeProvider 校验默认启用免密钥源时聚合器可用。
func TestAggregatorFreeProvider(t *testing.T) {
	agg := NewAggregator(&Config{EnableFreeProviders: true})
	if got := agg.AvailableProviders(); len(got) != 1 || got[0] != "caiyun" {
		t.Fatalf("providers = %v", got)
	}
}
