package weather

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// OpenMeteoProvider 使用 Open-Meteo 免密钥公开接口，覆盖全球当前、预报与历史归档。
//
// 依据 https://open-meteo.com 的公开非商业免费额度：不需要 API Key，
// 通过 geocoding-api 把城市名解析为经纬度后再取数。
type OpenMeteoProvider struct {
	geocodingEndpoint string
	forecastEndpoint  string
	archiveEndpoint   string
	client            *http.Client
}

// NewOpenMeteoProvider 创建 Open-Meteo 适配器。
func NewOpenMeteoProvider() *OpenMeteoProvider {
	return &OpenMeteoProvider{
		geocodingEndpoint: "https://geocoding-api.open-meteo.com/v1/search",
		forecastEndpoint:  "https://api.open-meteo.com/v1/forecast",
		archiveEndpoint:   "https://archive-api.open-meteo.com/v1/archive",
		client:            newClient(),
	}
}

func (p *OpenMeteoProvider) Name() string { return "openmeteo" }

func (p *OpenMeteoProvider) Capabilities() Capabilities {
	return Capabilities{Current: true, Forecast: true, History: true}
}

// coordinates 解析请求坐标：显式经纬度优先，城市名走 geocoding，行政区划编码不支持。
func (p *OpenMeteoProvider) coordinates(ctx context.Context, req *WeatherRequest) (float64, float64, string, error) {
	if req.Latitude != 0 || req.Longitude != 0 {
		if req.Latitude == 0 || req.Longitude == 0 {
			return 0, 0, "", fmt.Errorf("openmeteo: latitude and longitude must be provided together")
		}
		return req.Latitude, req.Longitude, fmt.Sprintf("%.4f,%.4f", req.Latitude, req.Longitude), nil
	}
	location := trimmed(req.Location)
	if location == "" || isNumericCode(location) {
		return 0, 0, "", ErrLocationUnsupported
	}
	query := url.Values{}
	query.Set("name", location)
	query.Set("count", "1")
	query.Set("language", "zh")
	query.Set("format", "json")
	var payload struct {
		Results []struct {
			Name      string  `json:"name"`
			Admin1    string  `json:"admin1"`
			Country   string  `json:"country"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	if err := getJSON(ctx, p.client, p.geocodingEndpoint+"?"+query.Encode(), nil, &payload); err != nil {
		return 0, 0, "", err
	}
	if len(payload.Results) == 0 {
		return 0, 0, "", fmt.Errorf("openmeteo: location not found")
	}
	found := payload.Results[0]
	label := strings.TrimSpace(strings.Join([]string{found.Country, found.Admin1, found.Name}, " "))
	if label == "" {
		label = location
	}
	return found.Latitude, found.Longitude, label, nil
}

func (p *OpenMeteoProvider) Fetch(ctx context.Context, req *WeatherRequest) (*WeatherResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("openmeteo: request is required")
	}
	latitude, longitude, label, err := p.coordinates(ctx, req)
	if err != nil {
		return nil, err
	}
	response := &WeatherResponse{Provider: p.Name(), Resolved: label, Attribution: "Open-Meteo (CC BY 4.0)"}
	if req.History {
		history, err := p.history(ctx, latitude, longitude, req)
		if err != nil {
			return nil, err
		}
		response.History = history
	}
	if req.Days >= 0 {
		forecast, current, err := p.forecast(ctx, latitude, longitude, forecastDays(req))
		if err != nil {
			return nil, err
		}
		response.Forecast = forecast
		response.Current = current
	}
	return response, nil
}

func forecastDays(req *WeatherRequest) int {
	days := req.Days
	if days <= 0 {
		days = 3
	}
	if days > 16 {
		days = 16
	}
	return days
}

type openMeteoDaily struct {
	Time             []string  `json:"time"`
	WeatherCode      []int     `json:"weather_code"`
	TemperatureMax   []float64 `json:"temperature_2m_max"`
	TemperatureMin   []float64 `json:"temperature_2m_min"`
	PrecipitationSum []float64 `json:"precipitation_sum"`
	WindDirection    []int     `json:"wind_direction_10m_dominant"`
}

type openMeteoCurrent struct {
	Time          string  `json:"time"`
	Temperature   float64 `json:"temperature_2m"`
	FeelsLike     float64 `json:"apparent_temperature"`
	Humidity      float64 `json:"relative_humidity_2m"`
	WindSpeed     float64 `json:"wind_speed_10m"`
	WindDirection float64 `json:"wind_direction_10m"`
	WeatherCode   int     `json:"weather_code"`
}

func (p *OpenMeteoProvider) forecast(ctx context.Context, latitude, longitude float64, days int) ([]DailyWeather, *CurrentWeather, error) {
	query := url.Values{}
	query.Set("latitude", strconv.FormatFloat(latitude, 'f', 4, 64))
	query.Set("longitude", strconv.FormatFloat(longitude, 'f', 4, 64))
	query.Set("current", "temperature_2m,relative_humidity_2m,apparent_temperature,wind_speed_10m,wind_direction_10m,weather_code")
	query.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,wind_direction_10m_dominant")
	query.Set("timezone", "auto")
	query.Set("forecast_days", strconv.Itoa(days))
	var payload struct {
		Current openMeteoCurrent `json:"current"`
		Daily   openMeteoDaily   `json:"daily"`
	}
	if err := getJSON(ctx, p.client, p.forecastEndpoint+"?"+query.Encode(), nil, &payload); err != nil {
		return nil, nil, err
	}
	current := &CurrentWeather{
		ObservedAt:   payload.Current.Time,
		TemperatureC: payload.Current.Temperature,
		FeelsLikeC:   payload.Current.FeelsLike,
		Humidity:     payload.Current.Humidity,
		WindSpeedKmh: payload.Current.WindSpeed,
		WindDir:      windDirection(payload.Current.WindDirection),
		Condition:    weatherCodeText(payload.Current.WeatherCode),
		Code:         "wmo:" + strconv.Itoa(payload.Current.WeatherCode),
	}
	return buildDaily(payload.Daily.Time, payload.Daily.WeatherCode, payload.Daily.TemperatureMax, payload.Daily.TemperatureMin, payload.Daily.PrecipitationSum, payload.Daily.WindDirection), current, nil
}

func (p *OpenMeteoProvider) history(ctx context.Context, latitude, longitude float64, req *WeatherRequest) ([]DailyWeather, error) {
	start, end, err := historyRange(req)
	if err != nil {
		return nil, fmt.Errorf("openmeteo: %w", err)
	}
	query := url.Values{}
	query.Set("latitude", strconv.FormatFloat(latitude, 'f', 4, 64))
	query.Set("longitude", strconv.FormatFloat(longitude, 'f', 4, 64))
	query.Set("start_date", start)
	query.Set("end_date", end)
	query.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,wind_direction_10m_dominant")
	query.Set("timezone", "auto")
	var payload struct {
		Daily openMeteoDaily `json:"daily"`
	}
	if err := getJSON(ctx, p.client, p.archiveEndpoint+"?"+query.Encode(), nil, &payload); err != nil {
		return nil, err
	}
	return buildDaily(payload.Daily.Time, payload.Daily.WeatherCode, payload.Daily.TemperatureMax, payload.Daily.TemperatureMin, payload.Daily.PrecipitationSum, payload.Daily.WindDirection), nil
}

// historyRange 解析历史区间；未指定时取最近一个完整自然月。
func historyRange(req *WeatherRequest) (string, string, error) {
	start, end := trimmed(req.Start), trimmed(req.End)
	if start == "" || end == "" {
		now := time.Now().UTC()
		firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		lastMonth := firstOfMonth.AddDate(0, -1, 0)
		start = lastMonth.Format("2006-01-02")
		end = firstOfMonth.AddDate(0, 0, -1).Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", start); err != nil {
		return "", "", fmt.Errorf("start must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", end); err != nil {
		return "", "", fmt.Errorf("end must be YYYY-MM-DD")
	}
	return start, end, nil
}

func buildDaily(dates []string, codes []int, highs, lows, precipitation []float64, windDirectionDegrees []int) []DailyWeather {
	daily := make([]DailyWeather, 0, len(dates))
	for index, date := range dates {
		daily = append(daily, DailyWeather{
			Date:          date,
			Week:          weekday(date),
			HighC:         at(highs, index),
			LowC:          at(lows, index),
			Condition:     weatherCodeText(atInt(codes, index)),
			WindDir:       windDirection(float64(atInt(windDirectionDegrees, index))),
			Precipitation: at(precipitation, index),
		})
	}
	return daily
}

func at(values []float64, index int) float64 {
	if index < len(values) {
		return values[index]
	}
	return 0
}

func atInt(values []int, index int) int {
	if index < len(values) {
		return values[index]
	}
	return 0
}

func weekday(date string) string {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ""
	}
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[int(parsed.Weekday())]
}

// windDirection 把角度换算为中文方位。
func windDirection(degrees float64) string {
	names := [...]string{"北风", "东北风", "东风", "东南风", "南风", "西南风", "西风", "西北风"}
	index := int((degrees+22.5)/45) % 8
	if index < 0 {
		index += 8
	}
	return names[index]
}

// weatherCodeText 把 WMO 天气代码翻译为中文描述。
func weatherCodeText(code int) string {
	switch code {
	case 0:
		return "晴"
	case 1:
		return "少云"
	case 2:
		return "多云"
	case 3:
		return "阴"
	case 45, 48:
		return "雾"
	case 51, 53, 55, 56, 57:
		return "毛毛雨"
	case 61, 63, 65, 66, 67:
		return "雨"
	case 71, 73, 75, 77:
		return "雪"
	case 80, 81, 82:
		return "阵雨"
	case 85, 86:
		return "阵雪"
	case 95:
		return "雷阵雨"
	case 96, 99:
		return "雷阵雨伴冰雹"
	default:
		return ""
	}
}
