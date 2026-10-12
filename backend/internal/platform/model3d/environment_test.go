package model3d

import "testing"

func TestConfigFromEnvironmentReadsAllProviders(t *testing.T) {
	t.Setenv(EnvEnabled, "true")
	t.Setenv(EnvPriority, "tripo,meshy")
	t.Setenv(EnvTimeoutSeconds, "45")
	t.Setenv(EnvMeshyAPIKey, "msy_env")
	t.Setenv(EnvMeshyDefaultModel, "meshy-4")
	t.Setenv(EnvTripoAPIKey, "tsk_env")
	t.Setenv(EnvTripoBaseURL, "https://tripo.example/v2/openapi")
	t.Setenv(EnvHi3DClientID, "cid")
	t.Setenv(EnvHi3DClientSecret, "secret")
	t.Setenv(EnvHi3DResolution, "1536pro")
	t.Setenv(EnvHi3DFaceLimit, "2000000")

	cfg := ConfigFromEnvironment()
	if !cfg.Configured() {
		t.Fatal("expected configured")
	}
	if cfg.TimeoutSeconds != 45 {
		t.Fatalf("timeout = %d", cfg.TimeoutSeconds)
	}
	if len(cfg.Priority) != 2 || cfg.Priority[0] != "tripo" {
		t.Fatalf("priority = %v", cfg.Priority)
	}
	if cfg.Meshy == nil || cfg.Meshy.APIKey != "msy_env" || cfg.Meshy.DefaultModel != "meshy-4" {
		t.Fatalf("meshy = %+v", cfg.Meshy)
	}
	if cfg.Tripo == nil || cfg.Tripo.BaseURL != "https://tripo.example/v2/openapi" {
		t.Fatalf("tripo = %+v", cfg.Tripo)
	}
	if cfg.Hi3D == nil || cfg.Hi3D.ClientSecret != "secret" || cfg.Hi3D.Resolution != "1536pro" || cfg.Hi3D.FaceLimit != 2000000 {
		t.Fatalf("hi3d = %+v", cfg.Hi3D)
	}
}

func TestConfigFromEnvironmentDisabledWithoutCredentials(t *testing.T) {
	t.Setenv(EnvMeshyAPIKey, "")
	t.Setenv(EnvTripoAPIKey, "")
	t.Setenv(EnvHi3DClientID, "")
	cfg := ConfigFromEnvironment()
	if cfg.Configured() {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.Enabled {
		t.Fatal("empty environment must disable the aggregator")
	}
}

func TestConfigFromEnvironmentHonoursExplicitDisable(t *testing.T) {
	t.Setenv(EnvEnabled, "false")
	t.Setenv(EnvMeshyAPIKey, "msy_env")
	cfg := ConfigFromEnvironment()
	if cfg.Enabled || cfg.Configured() {
		t.Fatalf("cfg = %+v", cfg)
	}
	if aggregator := NewAggregator(cfg); aggregator.Enabled() {
		t.Fatal("aggregator must honour MODEL3D_ENABLED=false")
	}
}
