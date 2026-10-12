//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestDoubaoProfileProxy(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		require.Equal(t, "Bearer private-adapter-key", r.Header.Get("Authorization"))
		switch r.Method {
		case http.MethodGet:
			require.Equal(t, "/internal/profiles", r.URL.Path)
			_, _ = w.Write([]byte(`{"profiles":["default","alice"]}`))
		case http.MethodPost:
			require.Equal(t, "/internal/profiles/alice", r.URL.Path)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"cookies":{"sessionid":"fixture"}}`, string(body))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"profile":"alice"}`))
		case http.MethodDelete:
			require.Equal(t, "/internal/profiles/alice", r.URL.Path)
			_, _ = w.Write([]byte(`{"deleted":true}`))
		}
	}))
	defer server.Close()
	SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, DesktopURL: server.URL + "/v1/", DesktopKey: "private-adapter-key"})
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
	profiles, err := ListDoubaoSessionProfiles(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"default", "alice"}, profiles)
	require.NoError(t, UploadDoubaoSessionProfile(context.Background(), "alice", []byte(`{"cookies":{"sessionid":"fixture"}}`)))
	require.NoError(t, DeleteDoubaoSessionProfile(context.Background(), "alice"))
	require.Equal(t, []string{http.MethodGet, http.MethodPost, http.MethodDelete}, methods)
}

func TestDoubaoProfileProxyRejectsUnsafeInputAndSanitizesFailures(t *testing.T) {
	for _, profile := range []string{"", "default", "../a", "_a", strings.Repeat("a", 65)} {
		require.Error(t, UploadDoubaoSessionProfile(context.Background(), profile, []byte("{}")))
		require.Error(t, DeleteDoubaoSessionProfile(context.Background(), profile))
	}
	for _, snapshot := range [][]byte{nil, []byte(strings.Repeat("x", (64<<10)+1))} {
		require.Error(t, UploadDoubaoSessionProfile(context.Background(), "alice", snapshot))
	}
	for _, status := range []int{400, 401, 404, 429, 500, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"message":"cookie-secret-must-not-leak"}}`))
			}))
			defer server.Close()
			SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, DesktopURL: server.URL, DesktopKey: "test-key"})
			_, err := ListDoubaoSessionProfiles(context.Background())
			require.Error(t, err)
			require.NotContains(t, err.Error(), "cookie-secret")
			if status == 429 {
				require.Equal(t, http.StatusTooManyRequests, infraerrors.Code(err))
			}
		})
	}
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
}

func TestDoubaoProfileListRejectsUnexpectedResponse(t *testing.T) {
	for _, body := range []string{`{}`, `{"profiles":["../a"]}`, `{"profiles":["default","../a"]}`, "invalid"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		SetBuiltinAdapterConfig(&config.BuiltinAdapterConfig{Enabled: true, DesktopURL: server.URL, DesktopKey: "test-key"})
		_, err := ListDoubaoSessionProfiles(context.Background())
		require.Error(t, err)
		server.Close()
	}
	t.Cleanup(func() { SetBuiltinAdapterConfig(nil) })
}
