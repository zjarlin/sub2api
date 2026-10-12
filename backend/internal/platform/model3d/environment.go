package model3d

import (
	"os"
	"strconv"
	"strings"
)

// Config 是 3D 服务商聚合配置。凭据只保存在服务端，接口响应里仅回显是否已配置。
type Config struct {
	// ProxyURL 是账号绑定的出站代理，仅供服务端传入。
	ProxyURL string `json:"-"`
	// Enabled 总开关；为 false 时聚合器不注册任何服务商。
	Enabled bool `json:"enabled,omitempty"`
	// Priority 是自动选择服务商的优先级；未列出的服务商排在后面。
	Priority []string `json:"priority,omitempty"`
	// TimeoutSeconds 是单次上游请求的等待上限（秒），0 表示默认 60 秒。
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// TaskHandleTTLSeconds 是网关任务句柄（含签名）的有效期（秒），0 表示默认 7 天。
	TaskHandleTTLSeconds int `json:"task_handle_ttl_seconds,omitempty"`

	Meshy *MeshyConfig `json:"meshy,omitempty"`
	Tripo *TripoConfig `json:"tripo,omitempty"`
	Hi3D  *Hi3DConfig  `json:"hi3d,omitempty"`
}

// MeshyConfig 配置 Meshy（api.meshy.ai）的 API Key。
type MeshyConfig struct {
	APIKey       string `json:"api_key,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
}

// TripoConfig 配置 Tripo（api.tripo3d.ai）的 API Key。
type TripoConfig struct {
	APIKey       string `json:"api_key,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
}

// Hi3DConfig 配置 Hi3D（Hitem3D，api.hitem3d.ai）的 client_id/client_secret。
type Hi3DConfig struct {
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
	// Resolution 默认分辨率，例如 v3.0 的 2048quality / 2048master。
	Resolution string `json:"resolution,omitempty"`
	// FaceLimit 默认面数，留 0 使用服务商默认值。
	FaceLimit int `json:"face_limit,omitempty"`
}

// 环境变量名统一使用 MODEL3D_ / 服务商前缀，便于部署时按服务商逐个开启。
const (
	EnvEnabled        = "MODEL3D_ENABLED"
	EnvPriority       = "MODEL3D_PRIORITY"
	EnvTimeoutSeconds = "MODEL3D_TIMEOUT_SECONDS"
	EnvHandleTTL      = "MODEL3D_TASK_HANDLE_TTL_SECONDS"

	EnvMeshyAPIKey       = "MESHY_API_KEY"
	EnvMeshyBaseURL      = "MESHY_BASE_URL"
	EnvMeshyDefaultModel = "MESHY_MODEL"

	EnvTripoAPIKey       = "TRIPO_API_KEY"
	EnvTripoBaseURL      = "TRIPO_BASE_URL"
	EnvTripoDefaultModel = "TRIPO_MODEL"

	EnvHi3DClientID     = "HI3D_CLIENT_ID"
	EnvHi3DClientSecret = "HI3D_CLIENT_SECRET"
	EnvHi3DBaseURL      = "HI3D_BASE_URL"
	EnvHi3DDefaultModel = "HI3D_MODEL"
	EnvHi3DResolution   = "HI3D_RESOLUTION"
	EnvHi3DFaceLimit    = "HI3D_FACE_LIMIT"
)

// ConfigFromEnvironment 读取部署环境变量。未配置任何服务商凭据时返回禁用配置，
// 与 translate 的做法一致：缺省不暴露空端点，避免误用。
func ConfigFromEnvironment() *Config {
	cfg := &Config{Enabled: true}
	if v := strings.TrimSpace(os.Getenv(EnvEnabled)); v != "" {
		cfg.Enabled = !isFalse(v)
	}
	if v := strings.TrimSpace(os.Getenv(EnvPriority)); v != "" {
		for _, part := range strings.Split(v, ",") {
			if key := strings.ToLower(strings.TrimSpace(part)); key != "" {
				cfg.Priority = append(cfg.Priority, key)
			}
		}
	}
	cfg.TimeoutSeconds = envInt(EnvTimeoutSeconds, 0)
	cfg.TaskHandleTTLSeconds = envInt(EnvHandleTTL, 0)

	if key := strings.TrimSpace(os.Getenv(EnvMeshyAPIKey)); key != "" {
		cfg.Meshy = &MeshyConfig{
			APIKey:       key,
			BaseURL:      strings.TrimSpace(os.Getenv(EnvMeshyBaseURL)),
			DefaultModel: strings.TrimSpace(os.Getenv(EnvMeshyDefaultModel)),
		}
	}
	if key := strings.TrimSpace(os.Getenv(EnvTripoAPIKey)); key != "" {
		cfg.Tripo = &TripoConfig{
			APIKey:       key,
			BaseURL:      strings.TrimSpace(os.Getenv(EnvTripoBaseURL)),
			DefaultModel: strings.TrimSpace(os.Getenv(EnvTripoDefaultModel)),
		}
	}
	if id := strings.TrimSpace(os.Getenv(EnvHi3DClientID)); id != "" {
		cfg.Hi3D = &Hi3DConfig{
			ClientID:     id,
			ClientSecret: strings.TrimSpace(os.Getenv(EnvHi3DClientSecret)),
			BaseURL:      strings.TrimSpace(os.Getenv(EnvHi3DBaseURL)),
			DefaultModel: strings.TrimSpace(os.Getenv(EnvHi3DDefaultModel)),
			Resolution:   strings.TrimSpace(os.Getenv(EnvHi3DResolution)),
			FaceLimit:    envInt(EnvHi3DFaceLimit, 0),
		}
	}
	if cfg.Meshy == nil && cfg.Tripo == nil && cfg.Hi3D == nil {
		cfg.Enabled = false
	}
	return cfg
}

// Configured 判断是否至少配置了一个可用服务商。
func (c *Config) Configured() bool {
	if c == nil || !c.Enabled {
		return false
	}
	return c.Meshy != nil || c.Tripo != nil || c.Hi3D != nil
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func isFalse(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off", "disabled":
		return true
	default:
		return false
	}
}
