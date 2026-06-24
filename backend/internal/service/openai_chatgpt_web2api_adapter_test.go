//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeChatGPTWeb2APIModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model string
		want  string
	}{
		{"", "auto"},
		{"auto", "auto"},
		{"gpt-5.5", "gpt-5-5"},
		{"gpt-5.5-thinking", "gpt-5-5-thinking"},
		{"gpt-5.3", "gpt-5-3"},
		{"gpt-5.3-mini", "gpt-5-3-mini"},
		{"gpt-4o", "auto"},
		{"gpt-4", "gpt-5"},
		{"gpt-3.5-turbo", "gpt-5-mini"},
		{"custom-model", "custom-model"},
		{"gpt-5.5[1m]", "gpt-5-5"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, normalizeChatGPTWeb2APIModel(tt.model))
		})
	}
}

func TestBuildChatGPTWeb2APIHealthURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base string
		want string
	}{
		{"", "http://127.0.0.1:8080/health"},
		{"http://127.0.0.1:8080/v1", "http://127.0.0.1:8080/health"},
		{"http://localhost:8080/v1/chat/completions", "http://localhost:8080/health"},
		{"http://host.docker.internal:8080/chat/completions", "http://host.docker.internal:8080/health"},
		{"http://127.0.0.1:8080/prefix/v1", "http://127.0.0.1:8080/prefix/health"},
	}

	for _, tt := range tests {
		t.Run(tt.base, func(t *testing.T) {
			t.Parallel()
			got, err := buildChatGPTWeb2APIHealthURL(tt.base)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseChatGPTWeb2APIHealth(t *testing.T) {
	t.Parallel()

	status, err := parseChatGPTWeb2APIHealth([]byte(`{"status":"waiting","cdp_connected":false,"requests_served":7}`))
	require.NoError(t, err)
	require.Equal(t, "waiting", status.Status)
	require.False(t, status.CDPConnected)
	require.Equal(t, int64(7), status.RequestsServed)
	require.Contains(t, chatGPTWeb2APIHealthErrorMessage(status), "Chrome CDP")

	status, err = parseChatGPTWeb2APIHealth([]byte(`{"status":"ok","cdp_connected":true,"requests_served":8}`))
	require.NoError(t, err)
	require.Empty(t, chatGPTWeb2APIHealthErrorMessage(status))
}
