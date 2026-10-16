package weather

import (
	"os"
	"strings"
)

// ConfigFromEnvironment 读取天气服务配置，默认启用内置免密钥公开源。
func ConfigFromEnvironment() *Config {
	cfg := &Config{EnableFreeProviders: os.Getenv("WEATHER_FREE_PROVIDERS") != "false"}
	if priority := strings.TrimSpace(os.Getenv("WEATHER_PRIORITY")); priority != "" {
		for _, name := range strings.Split(priority, ",") {
			if value := strings.TrimSpace(name); value != "" {
				cfg.Priority = append(cfg.Priority, value)
			}
		}
	}
	return cfg
}
