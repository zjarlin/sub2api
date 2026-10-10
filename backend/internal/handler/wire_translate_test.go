package handler

import (
	"reflect"
	"testing"
)

func TestProvideTranslateAggregatorGoogleWebOptIn(t *testing.T) {
	for _, key := range []string{"TRANSLATE_CAIYUN_TOKEN", "TRANSLATE_TENCENT_SECRET_ID", "TRANSLATE_BAIDU_APP_ID", "TRANSLATE_YOUDAO_APP_KEY", "TRANSLATE_GOOGLE_WEB_PROXY_URL", "TRANSLATE_MYMEMORY", "TRANSLATE_LIBRETRANSLATE_URL", "TRANSLATE_HYMT_URL"} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct {
		name, free, google string
		want               []string
	}{
		{"default", "", "", []string{"caiyun"}},
		{"opt in", "", "true", []string{"caiyun", "google_web"}},
		{"disabled", "", "false", []string{"caiyun"}},
		{"free disabled", "false", "true", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRANSLATE_FREE_PROVIDERS", tc.free)
			t.Setenv("TRANSLATE_GOOGLE_WEB", tc.google)
			got := ProvideTranslateAggregator().AvailableProviders()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("providers = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProvideTranslateAggregatorPublicProviders(t *testing.T) {
	for _, key := range []string{"TRANSLATE_CAIYUN_TOKEN", "TRANSLATE_TENCENT_SECRET_ID", "TRANSLATE_YOUDAO_APP_KEY", "TRANSLATE_GOOGLE_WEB"} {
		t.Setenv(key, "")
	}
	t.Setenv("TRANSLATE_FREE_PROVIDERS", "false")
	t.Setenv("TRANSLATE_BAIDU_APP_ID", "test-app")
	t.Setenv("TRANSLATE_BAIDU_SECRET", "test-secret")
	t.Setenv("TRANSLATE_MYMEMORY", "true")
	t.Setenv("TRANSLATE_LIBRETRANSLATE_URL", "http://libretranslate:5000")
	t.Setenv("TRANSLATE_HYMT_URL", "http://hymt:8080")
	got := ProvideTranslateAggregator().AvailableProviders()
	if !reflect.DeepEqual(got, []string{"mymemory", "libretranslate", "hymt", "baidu"}) {
		t.Fatalf("providers = %v", got)
	}
}
