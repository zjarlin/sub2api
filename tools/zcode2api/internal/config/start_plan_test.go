package config

import "testing"

func TestStartPlanConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		base string
		key  string
		ok   bool
	}{
		{"web login", "", "", true},
		{"explicit JWT", "https://zcode.z.ai/api/v1/zcode-plan/anthropic", "login-jwt", true},
		{"wrong plan endpoint", "https://open.bigmodel.cn/api/anthropic", "login-jwt", false},
		{"missing endpoint", "", "login-jwt", false},
		{"query credentials", "https://zcode.z.ai/api/v1/zcode-plan/anthropic?token=secret", "login-jwt", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Upstream.Plan = "start-plan"
			cfg.Upstream.APIKey = tc.key
			cfg.Upstream.BaseURL = tc.base
			if err := cfg.validate(); (err == nil) != tc.ok {
				t.Fatalf("validate = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
