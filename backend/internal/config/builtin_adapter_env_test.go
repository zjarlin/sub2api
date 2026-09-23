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
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	viper.SetDefault("builtin_adapter.enabled", false)
	viper.SetDefault("builtin_adapter.desktop_url", "")
	viper.SetDefault("builtin_adapter.traework_key", "")
	viper.SetDefault("builtin_adapter.workbuddy_key", "")
	viper.SetDefault("builtin_adapter.workbuddy_url", "")
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
}
