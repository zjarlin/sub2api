package translate

import (
	"context"
	"os"
	"testing"
)

// TestCaiyunLive 走真实彩云接口，默认跳过；设置 TRANSLATE_LIVE=1 时执行。
func TestCaiyunLive(t *testing.T) {
	if os.Getenv("TRANSLATE_LIVE") != "1" {
		t.Skip("set TRANSLATE_LIVE=1 to hit the real Caiyun API")
	}
	tr := NewCaiyunTranslator(nil)
	resp, err := tr.Translate(context.Background(), &TranslateRequest{
		Text: []string{"Hello, world!"}, SourceLang: "en", TargetLang: "zh-CN",
	})
	if err != nil {
		t.Fatalf("live translate failed: %v", err)
	}
	if len(resp.Translations) == 0 || resp.Translations[0].Text == "" {
		t.Fatalf("empty translation: %+v", resp)
	}
	t.Logf("caiyun: %q -> %q", "Hello, world!", resp.Translations[0].Text)
}
