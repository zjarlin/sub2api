package jev_api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadModelRoutesSystemOne(t *testing.T) {
	for _, model := range []string{ModelID, LayaModelID} {
		got, err := ReadModel([]byte(`{"model":"` + model + `","state":"example","questions":{}}`))
		require.NoError(t, err)
		require.Equal(t, model, got)
	}
	for _, request := range []string{
		`{"model":"gpt-5"}`,
		`{"model":"laya","MODEL":"typesafe/jev"}`,
		`{"model":"laya"} {"model":"typesafe/jev"}`,
		`{"MODEL":"laya"}`,
	} {
		_, err := ReadModel([]byte(request))
		require.Error(t, err, request)
	}
}

func TestRelayLayaForwardsWithoutClientAuthorization(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/systemone", r.URL.Path)
		require.Empty(t, r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"model":"laya","state":"test"}`, string(body))
		_, _ = w.Write([]byte(`{"answers":{}}`))
	}))
	defer upstream.Close()
	status, payload, err := RelayLaya(context.Background(), upstream.URL, []byte(`{"model":"laya","state":"test"}`), upstream.Client())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"answers":{}}`, string(payload))
}

func TestRelayLayaRejectsUnsafeTarget(t *testing.T) {
	for _, target := range []string{"https://example.com", "http://user:pass@localhost", "http://localhost/path", "http://localhost?token=secret", ""} {
		_, _, err := RelayLaya(context.Background(), target, nil, http.DefaultClient)
		require.Error(t, err, target)
	}
}
