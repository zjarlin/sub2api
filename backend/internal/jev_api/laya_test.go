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

func TestRewriteModelChangesOnlyTopLevelModel(t *testing.T) {
	body := []byte(`{"model":"typesafe/jev","state":"example","questions":{"urgent":{"type":"noul"}}}`)
	rewritten, err := RewriteModel(body, LayaModelID)
	require.NoError(t, err)
	require.JSONEq(t,
		`{"model":"laya","state":"example","questions":{"urgent":{"type":"noul"}}}`,
		string(rewritten),
	)

	// 只接受已校验的 System One 模型，避免把任意字符串写进上游请求。
	_, err = RewriteModel(body, "gpt-5")
	require.Error(t, err)
	// 重复 model 键必须拒绝，避免未知键覆盖语义。
	_, err = RewriteModel([]byte(`{"model":"typesafe/jev","model":"laya"}`), LayaModelID)
	require.Error(t, err)

	// 目标模型与现有模型相同时保持原字节，避免无意义的重新编码。
	same := []byte(`{"model":"laya","state":"x"}`)
	got, err := RewriteModel(same, LayaModelID)
	require.NoError(t, err)
	require.Equal(t, same, got)
}

func TestSystemOneFallbackChainRewritesAndRelaysToLaya(t *testing.T) {
	// 复刻网关隐式回退的关键链路：JEV 上游 502 后，
	// 同一份请求只替换 model 即转交 Laya，其它字段保持不变。
	jevUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/systemone", r.URL.Path)
		require.Equal(t, "Bearer jev-key", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"jev unavailable"}`))
	}))
	defer jevUpstream.Close()

	var layaBody string
	layaUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/systemone", r.URL.Path)
		require.Empty(t, r.Header.Get("Authorization"))
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		layaBody = string(raw)
		_, _ = w.Write([]byte(`{"model":"laya-rl-agent","answers":{}}`))
	}))
	defer layaUpstream.Close()

	body := []byte(`{"model":"typesafe/jev","state":"x","questions":{"urgent":{"type":"noul"}}}`)
	status, _, err := RelaySystemOne(context.Background(), jevUpstream.URL, "jev-key", body, jevUpstream.Client())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadGateway, status)

	rewritten, err := RewriteModel(body, LayaModelID)
	require.NoError(t, err)
	status, payload, err := RelaySystemOne(context.Background(), layaUpstream.URL, "", rewritten, layaUpstream.Client())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t,
		`{"model":"laya","state":"x","questions":{"urgent":{"type":"noul"}}}`,
		layaBody,
	)
	require.JSONEq(t, `{"model":"laya-rl-agent","answers":{}}`, string(payload))
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
