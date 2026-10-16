package handler

import (
	"reflect"
	"testing"
)

func TestProvideWeatherAggregatorFreeProviders(t *testing.T) {
	for _, tc := range []struct {
		name string
		free string
		want []string
	}{
		{"default", "", []string{"openmeteo", "wttr", "chinasky", "tianqi2345"}},
		{"explicit on", "true", []string{"openmeteo", "wttr", "chinasky", "tianqi2345"}},
		{"disabled", "false", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEATHER_FREE_PROVIDERS", tc.free)
			t.Setenv("WEATHER_PRIORITY", "")
			got := ProvideWeatherAggregator().AvailableProviders()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("providers = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProvideWeatherAggregatorPriorityOverride(t *testing.T) {
	t.Setenv("WEATHER_FREE_PROVIDERS", "true")
	t.Setenv("WEATHER_PRIORITY", "tianqi2345, wttr")
	got := ProvideWeatherAggregator().AvailableProviders()
	if len(got) != 4 || got[0] != "tianqi2345" || got[1] != "wttr" {
		t.Fatalf("providers = %v", got)
	}
}
