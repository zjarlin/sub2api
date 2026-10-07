package handler

import (
	"reflect"
	"testing"
)

func TestProvideTranslateAggregatorGoogleWebOptIn(t *testing.T) {
	for _, key := range []string{"TRANSLATE_CAIYUN_TOKEN", "TRANSLATE_TENCENT_SECRET_ID", "TRANSLATE_BAIDU_APP_ID", "TRANSLATE_YOUDAO_APP_KEY", "TRANSLATE_GOOGLE_WEB_PROXY_URL"} {
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
