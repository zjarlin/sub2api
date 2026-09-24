package jev_api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubProvider 实现 TargetProvider，便于在不依赖内容审计服务的情况下验证中继。
type stubProvider struct {
	baseURL string
	apiKey  string
	client  *http.Client
	err     error
}

func (s stubProvider) TypeSafeRelayTarget(context.Context) (string, string, *http.Client, error) {
	return s.baseURL, s.apiKey, s.client, s.err
}

func TestRelayForwardsSystemOneAndInjectsKey(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"push":{"type":"noul","noul":0.98}},"usage":{"input_tokens":42,"output_tokens":0}}`))
	}))
	defer upstream.Close()

	request := []byte(`{"model":"typesafe/jev","state":{"request":"推送代码"},"questions":{"push":{"type":"noul","instructions":"是否推送"}}}`)
	status, payload, err := Relay(context.Background(), stubProvider{baseURL: upstream.URL, apiKey: "relay-secret", client: upstream.Client()}, request)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "/v1/systemone", gotPath)
	require.Equal(t, "Bearer relay-secret", gotAuth)
	require.Equal(t, ModelID, gotBody["model"])
	require.NotNil(t, gotBody["questions"])

	var decoded struct {
		Model   string `json:"model"`
		Answers struct {
			Push struct {
				Type string  `json:"type"`
				Noul float64 `json:"noul"`
			} `json:"push"`
		} `json:"answers"`
	}
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Equal(t, "jev-1.13.0", decoded.Model)
	require.Equal(t, 0.98, decoded.Answers.Push.Noul)
}

func TestRelayCommandCodePath(t *testing.T) {
	for _, suffix := range []string{"/provider", "/provider/v1/systemone"} {
		t.Run(suffix, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/provider/v1/systemone", r.URL.Path)
				_, _ = w.Write([]byte(`{"answers":{}}`))
			}))
			defer upstream.Close()
			status, _, err := Relay(context.Background(), stubProvider{baseURL: upstream.URL + suffix, apiKey: "key", client: upstream.Client()}, []byte(`{"model":"typesafe/jev"}`))
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)
		})
	}
}

func TestCommandCodeHostSelectsProviderPath(t *testing.T) {
	require.Equal(t, commandCodePath, resolveSystemOnePath("api.commandcode.ai", ""))
	require.Equal(t, systemOnePath, resolveSystemOnePath("api.typesafe.ai", ""))
}

func TestRelayRejectsMissingKey(t *testing.T) {
	_, _, err := Relay(context.Background(), stubProvider{apiKey: "   ", client: http.DefaultClient}, []byte(`{}`))
	require.ErrorIs(t, err, ErrRelayUnavailable)
}

func TestRelayDoesNotLeakTransportError(t *testing.T) {
	_, _, err := Relay(context.Background(), stubProvider{apiKey: "k", client: &http.Client{Transport: failingTransport{}}}, []byte(`{}`))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-host")
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial secret-host: connection refused")
}

func TestHandlerRejectsNonObjectBody(t *testing.T) {
	h := NewHandler(stubProvider{apiKey: "k", client: http.DefaultClient})
	rec := httptest.NewRecorder()
	c, _ := newTestContext(rec, []byte(`[1,2,3]`))
	h.Relay(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandlerReturnsUnavailableWithoutKey(t *testing.T) {
	h := NewHandler(stubProvider{apiKey: "", client: http.DefaultClient})
	rec := httptest.NewRecorder()
	c, _ := newTestContext(rec, []byte(`{"model":"typesafe/jev"}`))
	h.Relay(c)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandlerRejectsUnrelatedModel(t *testing.T) {
	h := NewHandler(stubProvider{apiKey: "key", client: http.DefaultClient})
	for _, body := range [][]byte{[]byte(`{"model":"gpt-5"}`), []byte(`{"model":"jev-latest"}`), []byte(`{"model":"typesafe/jev","model":"gpt-5"}`), []byte(`{"model":"typesafe/jev","MODEL":"gpt-5"}`)} {
		rec := httptest.NewRecorder()
		c, _ := newTestContext(rec, body)
		h.Relay(c)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
}
