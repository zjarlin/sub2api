package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ChinaSkyProvider 使用中国天气网 d1.weather.com.cn 的免密钥公开页，覆盖国内当前观测与当日预报。
//
// 该页只接受中国天气网行政区划编码（例如洛阳 101180901），并要求携带 referer，
// 因此 location 为城市名时返回 ErrLocationUnsupported，由其它数据源处理。
type ChinaSkyProvider struct {
	currentEndpoint  string
	forecastEndpoint string
	client           *http.Client
}

// NewChinaSkyProvider 创建中国天气网适配器。
func NewChinaSkyProvider() *ChinaSkyProvider {
	return &ChinaSkyProvider{
		currentEndpoint:  "http://d1.weather.com.cn/sk_2d/",
		forecastEndpoint: "http://d1.weather.com.cn/dingzhi/",
		client:           newClient(),
	}
}

func (p *ChinaSkyProvider) Name() string { return "chinasky" }

func (p *ChinaSkyProvider) Capabilities() Capabilities {
	return Capabilities{Current: true, Forecast: true}
}

func (p *ChinaSkyProvider) Fetch(ctx context.Context, req *WeatherRequest) (*WeatherResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("chinasky: request is required")
	}
	if req.History {
		return nil, ErrLocationUnsupported
	}
	code := trimmed(req.Location)
	if !isNumericCode(code) {
		return nil, ErrLocationUnsupported
	}
	headers := map[string]string{
		"referer":    "http://www.weather.com.cn/",
		"user-agent": browserUserAgent,
	}
	response := &WeatherResponse{Provider: p.Name(), Attribution: "中国天气网 weather.com.cn"}

	body, err := get(ctx, p.client, p.currentEndpoint+code+".html", headers)
	if err != nil {
		return nil, err
	}
	observed, err := decodeDataSK(body)
	if err != nil {
		return nil, err
	}
	response.Resolved = observed.CityName
	response.Current = &CurrentWeather{
		ObservedAt:   observed.Time,
		TemperatureC: parseFloat(observed.Temp),
		Humidity:     parseFloat(strings.TrimSuffix(observed.Humidity, "%")),
		WindSpeedKmh: windSpeedKmh(observed.WindScale),
		WindDir:      observed.WindDir,
		Condition:    observed.Weather,
		Code:         observed.WeatherCode,
	}
	if req.Days >= 0 {
		forecastBody, err := get(ctx, p.client, p.forecastEndpoint+code+".html", headers)
		if err != nil {
			return nil, err
		}
		forecast, err := decodeCityDZ(forecastBody, code)
		if err != nil {
			return nil, err
		}
		if forecast.Date != "" {
			response.Forecast = append(response.Forecast, DailyWeather{
				Date:      forecast.Date,
				Week:      weekday(forecast.Date),
				HighC:     parseFloat(strings.TrimSuffix(forecast.High, "℃")),
				LowC:      parseFloat(strings.TrimSuffix(forecast.Low, "℃")),
				Condition: forecast.Weather,
				WindDir:   forecast.WindDir,
			})
		}
	}
	return response, nil
}

// windScale 把蒲福风级文本（例如 "3级"）换算为近似 km/h。
func windSpeedKmh(scale string) float64 {
	value := parseFloat(strings.TrimSuffix(trimmed(scale), "级"))
	if value <= 0 {
		return 0
	}
	return value * 5
}

type dataSK struct {
	CityName    string `json:"cityname"`
	Temp        string `json:"temp"`
	WindDir     string `json:"WD"`
	WindScale   string `json:"WS"`
	Humidity    string `json:"SD"`
	AQI         string `json:"aqi"`
	Weather     string `json:"weather"`
	WeatherCode string `json:"weathercode"`
	Time        string `json:"time"`
	Date        string `json:"date"`
}

type forecastDZ struct {
	Date    string
	High    string `json:"temp"`
	Low     string `json:"tempn"`
	Weather string `json:"weather"`
	WindDir string `json:"wd"`
	Issued  string `json:"fctime"`
}

func decodeDataSK(body []byte) (*dataSK, error) {
	object, err := extractJSONObject(string(body))
	if err != nil {
		return nil, fmt.Errorf("chinasky: %w", err)
	}
	var observed dataSK
	if err := json.Unmarshal([]byte(object), &observed); err != nil {
		return nil, fmt.Errorf("chinasky: invalid current weather payload")
	}
	if observed.CityName == "" && observed.Temp == "" {
		return nil, fmt.Errorf("chinasky: current weather payload is empty")
	}
	return &observed, nil
}

func decodeCityDZ(body []byte, code string) (*forecastDZ, error) {
	object, err := extractJSONObject(string(body))
	if err != nil {
		return nil, fmt.Errorf("chinasky: %w", err)
	}
	var payload struct {
		WeatherInfo forecastDZ `json:"weatherinfo"`
	}
	if err := json.Unmarshal([]byte(object), &payload); err != nil {
		return nil, fmt.Errorf("chinasky: invalid forecast payload")
	}
	info := payload.WeatherInfo
	if len(info.Issued) >= 8 {
		infoDate := info.Issued[:8]
		info.Issued = infoDate[0:4] + "-" + infoDate[4:6] + "-" + infoDate[6:8]
	}
	return &forecastDZ{
		Date:    info.Issued,
		High:    info.High,
		Low:     info.Low,
		Weather: info.Weather,
		WindDir: info.WindDir,
		Issued:  info.Issued,
	}, nil
}

// extractJSONObject 从 "var x ={...};var y=..." 形态的脚本里取出第一个 JSON 对象。
func extractJSONObject(text string) (string, error) {
	start := strings.Index(text, "{")
	if start < 0 {
		return "", fmt.Errorf("response has no JSON object")
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(text); index++ {
		character := text[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case character == '\\':
				escaped = true
			case character == '"':
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : index+1], nil
			}
		}
	}
	return "", fmt.Errorf("response JSON object is not terminated")
}

var _ = strconv.Itoa
