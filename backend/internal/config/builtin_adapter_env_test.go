package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestBuiltinAdapterEnvReachable(t *testing.T) {
	viper.Reset()
	setDefaults()
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
	t.Setenv("BUILTIN_ADAPTER_TELEAGENT_KEY", "teleagent-key")
	t.Setenv("BUILTIN_ADAPTER_TELEAGENT_URL", "http://teleagent.internal:7872")
	t.Setenv("BUILTIN_ADAPTER_QODER_CATALOG_SCRIPT", "/opt/qoder-catalog/fetch.mjs")
	t.Setenv("BUILTIN_ADAPTER_QODER_CATALOG_WASM", "/opt/qoder-catalog/wasm-lib.mjs")
	t.Setenv("BUILTIN_ADAPTER_QODER_CATALOG_NODE", "/usr/local/bin/node")
	t.Setenv("BUILTIN_ADAPTER_QODER_CATALOG_QUEUE_DIR", "/var/lib/qoder-catalog")
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
	viper.SetDefault("builtin_adapter.qoder_catalog_script", "")
	viper.SetDefault("builtin_adapter.qoder_catalog_wasm", "")
	viper.SetDefault("builtin_adapter.qoder_catalog_node", "")
	viper.SetDefault("builtin_adapter.qoder_catalog_queue_dir", "")
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
	if cfg.BuiltinAdapter.TeleagentKey != "teleagent-key" || cfg.BuiltinAdapter.TeleagentBaseURL() != "http://teleagent.internal:7872" {
		t.Fatal("TeleAgent adapter environment not mapped")
	}
	if cfg.BuiltinAdapter.QoderCatalogScript != "/opt/qoder-catalog/fetch.mjs" ||
		cfg.BuiltinAdapter.QoderCatalogWasm != "/opt/qoder-catalog/wasm-lib.mjs" ||
		cfg.BuiltinAdapter.QoderCatalogNode != "/usr/local/bin/node" ||
		cfg.BuiltinAdapter.QoderCatalogQueueDir != "/var/lib/qoder-catalog" {
		t.Fatalf("Qoder catalog sidecar environment not mapped: %+v", cfg.BuiltinAdapter)
	}
}

func TestTeleagentBaseURL(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"", "http://sub2api-teleagent:7872"},
		{"  ", "http://sub2api-teleagent:7872"},
		{" http://adapter:7872/v1 ", "http://adapter:7872/v1"},
	} {
		if got := (BuiltinAdapterConfig{TeleagentURL: tc.value}).TeleagentBaseURL(); got != tc.want {
			t.Errorf("TeleagentBaseURL(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
