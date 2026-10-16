package weather

import (
	"errors"
	"strings"
	"testing"
)

func TestParseHistoryTable(t *testing.T) {
	table := `<tbody><tr><td>2026-01-01 周三</td><td>10℃</td><td>-2℃</td><td>晴</td><td>东风 2级</td><td>50 优</td></tr>` +
		`<tr><td>2026-01-02 周四</td><td>8℃</td><td>-3℃</td><td>多云</td><td>北风 3级</td><td>62 良</td></tr></tbody>`
	days := parseHistoryTable(table)
	if len(days) != 2 {
		t.Fatalf("days = %d, want 2", len(days))
	}
	if days[0].Date != "2026-01-01" || days[0].HighC != 10 || days[0].LowC != -2 {
		t.Fatalf("first day = %+v", days[0])
	}
	if days[0].Week != "周三" {
		t.Fatalf("week = %q, want 周三", days[0].Week)
	}
	if days[0].AirQuality != 50 || days[1].AirQuality != 62 {
		t.Fatalf("air quality = %d/%d", days[0].AirQuality, days[1].AirQuality)
	}
	if days[1].WindDir != "北风 3级" {
		t.Fatalf("wind = %q", days[1].WindDir)
	}
}

func TestParseHistoryTableSkipsUnparsableRows(t *testing.T) {
	table := `<tbody><tr><td colspan="6">暂无数据</td></tr></tbody>`
	if days := parseHistoryTable(table); len(days) != 0 {
		t.Fatalf("days = %d, want 0", len(days))
	}
}

func TestExtractJSONObject(t *testing.T) {
	body := `var dataSK={"cityname":"洛阳","aqi":"103","rain":"0"};var other={"x":1}`
	object, err := extractJSONObject(body)
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	if object != `{"cityname":"洛阳","aqi":"103","rain":"0"}` {
		t.Fatalf("object = %s", object)
	}
	if _, err := extractJSONObject("no object here"); err == nil {
		t.Fatal("missing object must be rejected")
	}
}

func TestDecodeDataSKAndCityDZ(t *testing.T) {
	current, err := decodeDataSK([]byte(`var dataSK={"cityname":"洛阳","temp":"18.6","WD":"东北风","WS":"3级","SD":"78%","aqi":"103","weather":"多云","time":"16:20"}`))
	if err != nil {
		t.Fatalf("decode current failed: %v", err)
	}
	if current.CityName != "洛阳" || current.Temp != "18.6" || current.WindDir != "东北风" {
		t.Fatalf("current = %+v", current)
	}
	forecast, err := decodeCityDZ([]byte(`var cityDZ101180901 ={"weatherinfo":{"city":"101180901","cityname":"洛阳","fctime":"202610161100","temp":"22℃","tempn":"15℃","weather":"小雨","wd":"东风"}};var alarmDZ`), "101180901")
	if err != nil {
		t.Fatalf("decode forecast failed: %v", err)
	}
	if forecast.Date != "2026-10-16" || forecast.High != "22℃" || forecast.Low != "15℃" || forecast.Weather != "小雨" {
		t.Fatalf("forecast = %+v", forecast)
	}
}

func TestHistoryMonthsExpandsAndBoundsRange(t *testing.T) {
	months, err := historyMonths("2025-11-15", "2026-01-02")
	if err != nil {
		t.Fatalf("historyMonths failed: %v", err)
	}
	if len(months) != 3 || months[0].year != 2025 || months[0].month != 11 || months[2].month != 1 {
		t.Fatalf("months = %+v", months)
	}
	if _, err := historyMonths("2026-01-02", "2025-11-15"); err == nil {
		t.Fatal("reversed range must be rejected")
	}
	if _, err := historyMonths("2020-01-01", "2026-01-01"); err == nil {
		t.Fatal("range longer than the month cap must be rejected")
	}
}

func TestHistoryRangeDefaultsToLastCompleteMonth(t *testing.T) {
	start, end, err := historyRange(&WeatherRequest{Location: "57073", History: true})
	if err != nil {
		t.Fatalf("historyRange failed: %v", err)
	}
	if !strings.HasPrefix(start, "20") || !strings.HasPrefix(end, "20") || start > end {
		t.Fatalf("range = %s..%s", start, end)
	}
}

func TestIsNumericCode(t *testing.T) {
	if !isNumericCode("101180901") || !isNumericCode("57073") {
		t.Fatal("numeric codes must be detected")
	}
	for _, value := range []string{"", "洛阳", "Luoyang", "101-180901", "57073 "} {
		if isNumericCode(value) && value != "57073 " {
			t.Fatalf("%q must not be treated as numeric code", value)
		}
	}
}

func TestProvidersRejectUnsupportedLocations(t *testing.T) {
	if _, err := NewChinaSkyProvider().Fetch(nil, &WeatherRequest{Location: "洛阳"}); !errors.Is(err, ErrLocationUnsupported) {
		t.Fatalf("chinasky error = %v, want ErrLocationUnsupported", err)
	}
	if _, err := NewTianqi2345Provider().Fetch(nil, &WeatherRequest{Location: "洛阳"}); !errors.Is(err, ErrLocationUnsupported) {
		t.Fatalf("tianqi2345 error = %v, want ErrLocationUnsupported", err)
	}
	if _, err := NewWttrProvider().Fetch(nil, &WeatherRequest{Location: "101180901"}); !errors.Is(err, ErrLocationUnsupported) {
		t.Fatalf("wttr error = %v, want ErrLocationUnsupported", err)
	}
	if _, err := NewOpenMeteoProvider().Fetch(nil, &WeatherRequest{Location: "101180901"}); !errors.Is(err, ErrLocationUnsupported) {
		t.Fatalf("openmeteo error = %v, want ErrLocationUnsupported", err)
	}
}

func TestNewAggregatorHonoursFreeProviderSwitch(t *testing.T) {
	disabled := NewAggregator(&Config{EnableFreeProviders: false})
	if disabled.Enabled() || len(disabled.AvailableProviders()) != 0 {
		t.Fatalf("disabled aggregator = %+v", disabled.Providers())
	}
	enabled := NewAggregator(nil)
	if !enabled.Enabled() {
		t.Fatal("nil config must enable built-in free providers")
	}
	names := enabled.AvailableProviders()
	want := []string{"openmeteo", "wttr", "chinasky", "tianqi2345"}
	if len(names) != len(want) {
		t.Fatalf("providers = %v, want %v", names, want)
	}
	for index, name := range want {
		if names[index] != name {
			t.Fatalf("providers = %v, want %v", names, want)
		}
	}
}

func TestNewAggregatorHonoursPriorityOverride(t *testing.T) {
	aggregator := NewAggregator(&Config{EnableFreeProviders: true, Priority: []string{"tianqi2345", "wttr"}})
	names := aggregator.AvailableProviders()
	if len(names) < 2 || names[0] != "tianqi2345" || names[1] != "wttr" {
		t.Fatalf("providers = %v", names)
	}
}
