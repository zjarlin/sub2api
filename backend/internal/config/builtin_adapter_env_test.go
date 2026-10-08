package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestBuiltinAdapterEnvReachable(t *testing.T) {
	viper.Reset()
	t.Setenv("BUILTIN_ADAPTER_ENABLED", "true")
	t.Setenv("BUILTIN_ADAPTER_DESKTOP_URL", "http://sub2api-desktop:8080")
	t.Setenv("BUILTIN_ADAPTER_TRAEWORK_KEY", "shared-key")
	t.Setenv("BUILTIN_ADAPTER_WORKBUDDY_KEY", "workbuddy-key")
	t.Setenv("BUILTIN_ADAPTER_WORKBUDDY_URL", "http://sub2api-workbuddy:7863")
	t.Setenv("BUILTIN_ADAPTER_VIBEX_KEY", "vibex-key")
	t.Setenv("BUILTIN_ADAPTER_VIBEX_URL", "http://sub2api-vibex:7866")
	t.Setenv("BUILTIN_ADAPTER_DEEPSEEK_WEB_KEY", "deepseek-key")
	t.Setenv("BUILTIN_ADAPTER_DEEPSEEK_WEB_URL", "http://sub2api-deepseek-web:7867")
	t.Setenv("BUILTIN_ADAPTER_WINDSURF_KEY", "windsurf-key")
	t.Setenv("BUILTIN_ADAPTER_WINDSURF_URL", "http://sub2api-windsurf:7869")
	t.Setenv("BUILTIN_ADAPTER_MADAO_KEY", "madao-key")
	t.Setenv("BUILTIN_ADAPTER_MADAO_URL", "http://sub2api-madao:7870")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	viper.SetDefault("builtin_adapter.enabled", false)
	viper.SetDefault("builtin_adapter.desktop_url", "")
	viper.SetDefault("builtin_adapter.traework_key", "")
	viper.SetDefault("builtin_adapter.workbuddy_key", "")
	viper.SetDefault("builtin_adapter.workbuddy_url", "")
	viper.SetDefault("builtin_adapter.vibex_key", "")
	viper.SetDefault("builtin_adapter.vibex_url", "")
	viper.SetDefault("builtin_adapter.deepseek_web_key", "")
	viper.SetDefault("builtin_adapter.deepseek_web_url", "")
	viper.SetDefault("builtin_adapter.windsurf_key", "")
	viper.SetDefault("builtin_adapter.windsurf_url", "")
	viper.SetDefault("builtin_adapter.madao_key", "")
	viper.SetDefault("builtin_adapter.madao_url", "")
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.BuiltinAdapter.Enabled {
		t.Fatalf("enabled not read: %+v", cfg.BuiltinAdapter)
	}
	if cfg.BuiltinAdapter.DesktopURL != "http://sub2api-desktop:8080" {
		t.Fatalf("desktop url: %q", cfg.BuiltinAdapter.DesktopURL)
	}
	if cfg.BuiltinAdapter.TraeworkKey != "shared-key" {
		t.Fatalf("traework key: %q", cfg.BuiltinAdapter.TraeworkKey)
	}
	if cfg.BuiltinAdapter.WorkbuddyKey != "workbuddy-key" || cfg.BuiltinAdapter.WorkbuddyBaseURL() != "http://sub2api-workbuddy:7863" {
		t.Fatal("workbuddy adapter environment not mapped")
	}
	if cfg.BuiltinAdapter.VibexKey != "vibex-key" || cfg.BuiltinAdapter.VibexBaseURL() != "http://sub2api-vibex:7866" {
		t.Fatal("VibeX adapter environment not mapped")
	}
	if cfg.BuiltinAdapter.DeepseekWebKey != "deepseek-key" || cfg.BuiltinAdapter.DeepseekWebBaseURL() != "http://sub2api-deepseek-web:7867" {
		t.Fatal("DeepSeek web adapter environment not mapped")
	}
	if cfg.BuiltinAdapter.WindsurfKey != "windsurf-key" || cfg.BuiltinAdapter.WindsurfBaseURL() != "http://sub2api-windsurf:7869" {
		t.Fatal("Windsurf adapter environment not mapped")
	}
	if cfg.BuiltinAdapter.MadaoKey != "madao-key" || cfg.BuiltinAdapter.MadaoBaseURL() != "http://sub2api-madao:7870" {
		t.Fatal("Madao adapter environment not mapped")
	}
}
