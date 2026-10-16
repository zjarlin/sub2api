package weather

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestProvidersLive 打真实上游，默认跳过；仅用于人工验证适配器与上游是否仍兼容。
//
//	WEATHER_LIVE_TEST=1 go test ./internal/platform/weather -run TestProvidersLive -v
func TestProvidersLive(t *testing.T) {
	if os.Getenv("WEATHER_LIVE_TEST") != "1" {
		t.Skip("set WEATHER_LIVE_TEST=1 to run live upstream checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Run("openmeteo", func(t *testing.T) {
		response, err := NewOpenMeteoProvider().Fetch(ctx, &WeatherRequest{Location: "洛阳", Days: 3, History: true, Start: "2026-01-01", End: "2026-01-05"})
		if err != nil {
			t.Fatalf("fetch failed: %v", err)
		}
		if response.Current == nil || len(response.Forecast) == 0 || len(response.History) == 0 {
			t.Fatalf("response incomplete: %+v", response)
		}
		t.Logf("current=%.1fC %s forecast=%d history=%d", response.Current.TemperatureC, response.Resolved, len(response.Forecast), len(response.History))
	})

	t.Run("wttr", func(t *testing.T) {
		response, err := NewWttrProvider().Fetch(ctx, &WeatherRequest{Location: "Luoyang"})
		if err != nil {
			t.Fatalf("fetch failed: %v", err)
		}
		if response.Current == nil || len(response.Forecast) == 0 {
			t.Fatalf("response incomplete: %+v", response)
		}
		t.Logf("current=%.1fC %s forecast=%d", response.Current.TemperatureC, response.Current.Condition, len(response.Forecast))
	})

	t.Run("chinasky", func(t *testing.T) {
		response, err := NewChinaSkyProvider().Fetch(ctx, &WeatherRequest{Location: "101180901"})
		if err != nil {
			t.Fatalf("fetch failed: %v", err)
		}
		if response.Current == nil {
			t.Fatalf("response incomplete: %+v", response)
		}
		t.Logf("current=%.1fC %s %s forecast=%d", response.Current.TemperatureC, response.Resolved, response.Current.Condition, len(response.Forecast))
	})

	t.Run("aggregator fallback", func(t *testing.T) {
		aggregator := NewAggregator(nil)
		response, err := aggregator.Fetch(ctx, &WeatherRequest{Location: "洛阳", Days: 2})
		if err != nil {
			t.Fatalf("fetch failed: %v", err)
		}
		if response.Provider == "" || response.Current == nil {
			t.Fatalf("response incomplete: %+v", response)
		}
		t.Logf("provider=%s attempts=%d current=%.1fC", response.Provider, len(response.Attempts), response.Current.TemperatureC)

		history, err := aggregator.Fetch(ctx, &WeatherRequest{Location: "57073", History: true, Start: "2026-01-01", End: "2026-01-31"})
		if err != nil {
			t.Fatalf("history fetch failed: %v", err)
		}
		if len(history.History) == 0 {
			t.Fatalf("history response incomplete: %+v", history)
		}
		t.Logf("history provider=%s rows=%d", history.Provider, len(history.History))
	})

	t.Run("tianqi2345", func(t *testing.T) {
		response, err := NewTianqi2345Provider().Fetch(ctx, &WeatherRequest{Location: "57073", History: true, Start: "2026-01-01", End: "2026-01-31"})
		if err != nil {
			t.Fatalf("fetch failed: %v", err)
		}
		if len(response.History) == 0 {
			t.Fatalf("response incomplete: %+v", response)
		}
		t.Logf("history=%d first=%+v", len(response.History), response.History[0])
	})
}
