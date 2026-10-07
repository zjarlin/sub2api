package translate

import "os"

// ConfigFromEnvironment 保留既有部署配置，数据库未设置时以此作为默认值。
func ConfigFromEnvironment() *Config {
	cfg := &Config{EnableFreeProviders: os.Getenv("TRANSLATE_FREE_PROVIDERS") != "false"}
	if token := os.Getenv("TRANSLATE_CAIYUN_TOKEN"); token != "" {
		cfg.Caiyun = &CaiyunConfig{Token: token}
	}
	if cfg.EnableFreeProviders && os.Getenv("TRANSLATE_GOOGLE_WEB") == "true" {
		cfg.GoogleWeb = &GoogleWebConfig{ProxyURL: os.Getenv("TRANSLATE_GOOGLE_WEB_PROXY_URL")}
	}
	if os.Getenv("TRANSLATE_MYMEMORY") == "true" {
		cfg.MyMemory = &MyMemoryConfig{Email: os.Getenv("TRANSLATE_MYMEMORY_EMAIL"), APIKey: os.Getenv("TRANSLATE_MYMEMORY_API_KEY")}
	}
	if baseURL := os.Getenv("TRANSLATE_LIBRETRANSLATE_URL"); baseURL != "" {
		cfg.LibreTranslate = &LibreTranslateConfig{BaseURL: baseURL, APIKey: os.Getenv("TRANSLATE_LIBRETRANSLATE_API_KEY")}
	}
	if baseURL := os.Getenv("TRANSLATE_HYMT_URL"); baseURL != "" {
		cfg.HyMT = &HyMTConfig{BaseURL: baseURL, APIKey: os.Getenv("TRANSLATE_HYMT_API_KEY")}
	}
	if id := os.Getenv("TRANSLATE_TENCENT_SECRET_ID"); id != "" {
		cfg.Tencent = &TencentConfig{SecretID: id, SecretKey: os.Getenv("TRANSLATE_TENCENT_SECRET_KEY"), Region: os.Getenv("TRANSLATE_TENCENT_REGION")}
	}
	if id := os.Getenv("TRANSLATE_BAIDU_APP_ID"); id != "" {
		cfg.Baidu = &BaiduConfig{AppID: id, Secret: os.Getenv("TRANSLATE_BAIDU_SECRET")}
	}
	if key := os.Getenv("TRANSLATE_YOUDAO_APP_KEY"); key != "" {
		cfg.Youdao = &YoudaoConfig{AppKey: key, AppSecret: os.Getenv("TRANSLATE_YOUDAO_APP_SECRET")}
	}
	return cfg
}
