package weather

import "context"

// WeatherRequest 统一天气请求。
//
// Location 必填，可写城市名（中文或英文）、行政区划编码或 "纬度,经度"。
// 历史区间与预报天数互不冲突：同一个请求可以同时要当前、预报和历史。
type WeatherRequest struct {
	Location  string  `json:"location"`
	Days      int     `json:"days,omitempty"`
	History   bool    `json:"history,omitempty"`
	Start     string  `json:"start,omitempty"`
	End       string  `json:"end,omitempty"`
	Provider  string  `json:"provider,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
}

// WeatherResponse 统一天气响应；各字段按请求能力和数据源可用性填充。
type WeatherResponse struct {
	Provider    string          `json:"provider"`
	Resolved    string          `json:"resolved,omitempty"`
	Current     *CurrentWeather `json:"current,omitempty"`
	Forecast    []DailyWeather  `json:"forecast,omitempty"`
	History     []DailyWeather  `json:"history,omitempty"`
	Attempts    []Attempt       `json:"attempts,omitempty"`
	Attribution string          `json:"attribution,omitempty"`
}

// CurrentWeather 实时观测。
type CurrentWeather struct {
	ObservedAt   string  `json:"observed_at,omitempty"`
	TemperatureC float64 `json:"temperature_c"`
	FeelsLikeC   float64 `json:"feels_like_c,omitempty"`
	Humidity     float64 `json:"humidity,omitempty"`
	WindSpeedKmh float64 `json:"wind_speed_kmh,omitempty"`
	WindDir      string  `json:"wind_direction,omitempty"`
	Condition    string  `json:"condition,omitempty"`
	Code         string  `json:"code,omitempty"`
}

// DailyWeather 单日预报或历史记录。
type DailyWeather struct {
	Date          string  `json:"date"`
	Week          string  `json:"week,omitempty"`
	HighC         float64 `json:"high_c"`
	LowC          float64 `json:"low_c"`
	Condition     string  `json:"condition,omitempty"`
	WindDir       string  `json:"wind_direction,omitempty"`
	AirQuality    int     `json:"air_quality_index,omitempty"`
	Precipitation float64 `json:"precipitation_mm,omitempty"`
}

// Attempt 记录聚合器本次对各候选源的尝试结果。
type Attempt struct {
	Provider  string  `json:"provider"`
	Status    string  `json:"status"`
	LatencyMS int64   `json:"latency_ms,omitempty"`
	Score     float64 `json:"score,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

// Capabilities 描述数据源支持的能力，聚合器据此跳过不满足请求的候选。
type Capabilities struct {
	Current  bool
	Forecast bool
	History  bool
}

// Provider 天气数据源适配器接口。
type Provider interface {
	// Name 返回数据源名称，用于优先级配置与诊断输出。
	Name() string
	// Capabilities 返回该源支持的数据能力。
	Capabilities() Capabilities
	// Fetch 拉取并归一化天气数据。
	Fetch(ctx context.Context, req *WeatherRequest) (*WeatherResponse, error)
}

// Config 天气服务配置；只读取环境变量，密钥类源不进入此抽象。
type Config struct {
	// Priority 覆盖默认尝试顺序；未列出的源排在已列出源之后。
	Priority []string
	// EnableFreeProviders 启用内置免密钥公开源，默认启用。
	EnableFreeProviders bool
}

// CurrentAt 兼容旧调用：判断响应是否包含当前天气。
func (r *WeatherResponse) CurrentAt() *CurrentWeather {
	if r == nil {
		return nil
	}
	return r.Current
}
