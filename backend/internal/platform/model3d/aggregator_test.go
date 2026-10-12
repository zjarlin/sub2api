package model3d

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAggregatorDisabledWithoutCredentials(t *testing.T) {
	aggregator := NewAggregator(&Config{Enabled: true})
	if aggregator.Enabled() {
		t.Fatal("aggregator must stay disabled without credentials")
	}
	if len(aggregator.ProviderInfos()) != 0 {
		t.Fatal("no provider should be registered")
	}
	if _, err := aggregator.Create(context.Background(), &CreateRequest{Mode: ModeTextTo3D, Prompt: "x"}); err == nil || !strings.Contains(err.Error(), ErrNoProvider.Error()) {
		t.Fatalf("err = %v", err)
	}
}

func TestAggregatorPriorityAndModeFiltering(t *testing.T) {
	cfg := &Config{
		Enabled:  true,
		Priority: []string{"hi3d", "tripo"},
		Tripo:    &TripoConfig{APIKey: "tripo-key", BaseURL: "https://tripo.invalid"},
		Hi3D:     &Hi3DConfig{ClientID: "id", ClientSecret: "secret", BaseURL: "https://hi3d.invalid"},
	}
	aggregator := NewAggregator(cfg)
	if !aggregator.Enabled() {
		t.Fatal("expected enabled")
	}
	if keys := aggregator.ProviderKeys(); strings.Join(keys, ",") != "hi3d,tripo,meshy" {
		t.Fatalf("keys = %v", keys)
	}
	if available := strings.Join(aggregator.AvailableProviders(), ","); available != "hi3d,tripo" {
		t.Fatalf("available = %s", available)
	}
	infos := aggregator.ProviderInfos()
	if !infos[0].Default || infos[0].Key != "hi3d" {
		t.Fatalf("default provider = %+v", infos[0])
	}
	// hi3d 不支持文生 3D，文生候选只剩 tripo。
	textCandidates := aggregator.Candidates(ModeTextTo3D)
	if len(textCandidates) != 1 || textCandidates[0].Key() != "tripo" {
		t.Fatalf("text candidates = %v", textCandidates)
	}
	// 图生 3D 两家都支持，按优先级排序。
	imageCandidates := aggregator.Candidates(ModeImageTo3D)
	if len(imageCandidates) != 2 || imageCandidates[0].Key() != "hi3d" {
		t.Fatalf("image candidates = %v", imageCandidates)
	}
	health := aggregator.ProviderHealth()
	if health["meshy"] != "unconfigured" || health["tripo"] != "ready" {
		t.Fatalf("health = %v", health)
	}
}

func TestAggregatorExplicitProviderRespected(t *testing.T) {
	var tripoCalled, meshyCalled bool
	tripo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tripoCalled = true
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"tripo-task"}}`))
	}))
	defer tripo.Close()
	meshy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meshyCalled = true
		_, _ = w.Write([]byte(`{"result":"meshy-task"}`))
	}))
	defer meshy.Close()

	aggregator := NewAggregator(&Config{
		Enabled: true,
		Meshy:   &MeshyConfig{APIKey: "m", BaseURL: meshy.URL},
		Tripo:   &TripoConfig{APIKey: "t", BaseURL: tripo.URL},
	})
	task, err := aggregator.Create(context.Background(), &CreateRequest{Mode: ModeTextTo3D, Prompt: "x", Provider: "tripo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.Provider != "tripo" || task.ID != "tripo-task" || task.Status != StatusQueued {
		t.Fatalf("task = %+v", task)
	}
	if meshyCalled {
		t.Fatal("explicit provider must not fall through to another provider")
	}
	if !tripoCalled {
		t.Fatal("tripo must be called")
	}
}

func TestAggregatorDoesNotFailoverOnCreateError(t *testing.T) {
	var attempts int
	tripo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":500,"message":"boom"}`))
	}))
	defer tripo.Close()
	meshy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		_, _ = w.Write([]byte(`{"result":"should-not-be-used"}`))
	}))
	defer meshy.Close()

	aggregator := NewAggregator(&Config{
		Enabled:  true,
		Priority: []string{"tripo", "meshy"},
		Tripo:    &TripoConfig{APIKey: "t", BaseURL: tripo.URL},
		Meshy:    &MeshyConfig{APIKey: "m", BaseURL: meshy.URL},
	})
	if _, err := aggregator.Create(context.Background(), &CreateRequest{Mode: ModeTextTo3D, Prompt: "x"}); err == nil {
		t.Fatal("expected upstream failure")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, create must not fail over (duplicate charges)", attempts)
	}
}

func TestAggregatorUnsupportedModeAndUnknownProvider(t *testing.T) {
	aggregator := NewAggregator(&Config{Enabled: true, Hi3D: &Hi3DConfig{ClientID: "id", ClientSecret: "s", BaseURL: "https://hi3d.invalid"}})
	if _, err := aggregator.Create(context.Background(), &CreateRequest{Mode: ModeTextTo3D, Prompt: "x", Provider: "hi3d"}); err == nil || !strings.Contains(err.Error(), ErrUnsupported.Error()) {
		t.Fatalf("err = %v", err)
	}
	if _, err := aggregator.Create(context.Background(), &CreateRequest{Mode: ModeTextTo3D, Prompt: "x", Provider: "nope"}); err == nil || !strings.Contains(err.Error(), ErrProviderUnavailable.Error()) {
		t.Fatalf("err = %v", err)
	}
}

func TestAggregatorGetUsesModeAwareQueryForMeshy(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "t", "status": "IN_PROGRESS", "progress": 40})
	}))
	defer server.Close()
	aggregator := NewAggregator(&Config{Enabled: true, Meshy: &MeshyConfig{APIKey: "k", BaseURL: server.URL + "/openapi"}})
	task, err := aggregator.Get(context.Background(), "meshy", ModeImageTo3D, "t")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotPath != "/openapi/v1/image-to-3d/t" {
		t.Fatalf("path = %q", gotPath)
	}
	if task.Provider != "meshy" || task.Status != StatusRunning || task.Progress != 40 {
		t.Fatalf("task = %+v", task)
	}
}

func TestAggregatorBalanceDefaultsToFirstProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"balance":7}`))
	}))
	defer server.Close()
	aggregator := NewAggregator(&Config{Enabled: true, Meshy: &MeshyConfig{APIKey: "k", BaseURL: server.URL}})
	provider, balance, err := aggregator.Balance(context.Background(), "")
	if err != nil || provider != "meshy" || balance != 7 {
		t.Fatalf("provider=%q balance=%v err=%v", provider, balance, err)
	}
}

func TestOrderedPriorityDeduplicatesAndAppendsDefaults(t *testing.T) {
	order := orderedPriority([]string{" Hi3D ", "hi3d", "", "meshy"})
	if strings.Join(order, ",") != "hi3d,meshy,tripo" {
		t.Fatalf("order = %v", order)
	}
}
