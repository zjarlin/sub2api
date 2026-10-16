package weather

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// WttrProvider 使用 wttr.in 的免密钥公开 JSON 接口，覆盖全球当前与三日预报。
type WttrProvider struct {
	endpoint string
	client   *http.Client
}

// NewWttrProvider 创建 wttr.in 适配器。
func NewWttrProvider() *WttrProvider {
	return &WttrProvider{endpoint: "https://wttr.in", client: newClient()}
}

func (p *WttrProvider) Name() string { return "wttr" }

func (p *WttrProvider) Capabilities() Capabilities {
	return Capabilities{Current: true, Forecast: true}
}

func (p *WttrProvider) Fetch(ctx context.Context, req *WeatherRequest) (*WeatherResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("wttr: request is required")
	}
	if req.History {
		return nil, ErrLocationUnsupported
	}
	location := trimmed(req.Location)
	if location == "" {
		if req.Latitude == 0 && req.Longitude == 0 {
			return nil, fmt.Errorf("wttr: location is required")
		}
		location = strconv.FormatFloat(req.Latitude, 'f', 4, 64) + "," + strconv.FormatFloat(req.Longitude, 'f', 4, 64)
	} else if isNumericCode(location) {
		return nil, ErrLocationUnsupported
	}
	endpoint := p.endpoint + "/" + url.PathEscape(location) + "?format=j1&lang=zh"
	var payload struct {
		Current []struct {
			TempC           string `json:"temp_C"`
			FeelsLikeC      string `json:"FeelsLikeC"`
			Humidity        string `json:"humidity"`
			WindSpeedKmph   string `json:"windspeedKmph"`
			WindDirection   string `json:"winddir16Point"`
			ObservationTime string `json:"observation_time"`
			Description     []struct {
				Value string `json:"value"`
			} `json:"weatherDesc"`
		} `json:"current_condition"`
		Forecast []struct {
			Date      string `json:"date"`
			MaxTempC  string `json:"maxtempC"`
			MinTempC  string `json:"mintempC"`
			Sunrise   string `json:"sunrise"`
			TotalSnow string `json:"totalSnow_cm"`
		} `json:"weather"`
		Nearest []struct {
			AreaName []struct {
				Value string `json:"value"`
			} `json:"areaName"`
		} `json:"nearest_area"`
	}
	if err := getJSON(ctx, p.client, endpoint, nil, &payload); err != nil {
		return nil, err
	}
	if len(payload.Current) == 0 && len(payload.Forecast) == 0 {
		return nil, fmt.Errorf("wttr: response has no weather data")
	}
	response := &WeatherResponse{Provider: p.Name(), Attribution: "wttr.in"}
	if len(payload.Nearest) > 0 && len(payload.Nearest[0].AreaName) > 0 {
		response.Resolved = payload.Nearest[0].AreaName[0].Value
	}
	if len(payload.Current) > 0 {
		current := payload.Current[0]
		condition := ""
		if len(current.Description) > 0 {
			condition = current.Description[0].Value
		}
		response.Current = &CurrentWeather{
			ObservedAt:   current.ObservationTime,
			TemperatureC: parseFloat(current.TempC),
			FeelsLikeC:   parseFloat(current.FeelsLikeC),
			Humidity:     parseFloat(current.Humidity),
			WindSpeedKmh: parseFloat(current.WindSpeedKmph),
			WindDir:      current.WindDirection,
			Condition:    condition,
		}
	}
	for _, day := range payload.Forecast {
		response.Forecast = append(response.Forecast, DailyWeather{
			Date:  day.Date,
			Week:  weekday(day.Date),
			HighC: parseFloat(day.MaxTempC),
			LowC:  parseFloat(day.MinTempC),
		})
	}
	return response, nil
}

func parseFloat(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	return parsed
}
