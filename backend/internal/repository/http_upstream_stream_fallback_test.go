package repository

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type failingHTTP2Stream struct {
	err    error
	closed bool
}

func (b *failingHTTP2Stream) Read(p []byte) (int, error) {
	return copy(p, "data: partial\n\n"), b.err
}

func (b *failingHTTP2Stream) Close() error {
	b.closed = true
	return nil
}

func TestHTTPUpstreamStreamFailureFallsBackPerAccount(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"connection_lost", errors.New("http2: client connection lost"), upstreamProtocolModeOpenAIH1Fallback},
		{"stream_reset", errors.New("stream error: stream ID 1; INTERNAL_ERROR"), upstreamProtocolModeOpenAIH1Fallback},
		{"cancelled", context.Canceled, upstreamProtocolModeOpenAIH2},
		{"cancelled_stream", errors.Join(errors.New("http2: stream error"), context.Canceled), upstreamProtocolModeOpenAIH2},
		{"deadline", context.DeadlineExceeded, upstreamProtocolModeOpenAIH2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.OpenAIHTTP2 = config.GatewayOpenAIHTTP2Config{Enabled: true, AllowProxyFallbackToHTTP1: true}
			svc := NewHTTPUpstream(cfg).(*httpUpstreamService)
			entry, err := svc.getClientEntry("", 310, 1, service.HTTPUpstreamProfileOpenAI, false, false)
			require.NoError(t, err)
			body := &failingHTTP2Stream{err: tc.err}
			entry.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
			})
			req, err := http.NewRequestWithContext(service.WithHTTPUpstreamProfile(t.Context(), service.HTTPUpstreamProfileOpenAI), http.MethodPost, "https://provider.example/v1/chat/completions", nil)
			require.NoError(t, err)
			resp, err := svc.Do(req, "", 310, 1)
			require.NoError(t, err)
			require.False(t, svc.isOpenAIHTTP2FallbackActive(openAIHTTP2FallbackKey(directProxyKey, 310)), "响应头不能提前判定流读取结果")
			data, err := io.ReadAll(resp.Body)
			require.ErrorIs(t, err, tc.err)
			require.Equal(t, "data: partial\n\n", string(data))
			require.NoError(t, resp.Body.Close())
			require.True(t, body.closed)
			require.Zero(t, atomic.LoadInt64(&entry.inFlight))
			next, err := svc.getClientEntry("", 310, 1, service.HTTPUpstreamProfileOpenAI, false, false)
			require.NoError(t, err)
			require.Equal(t, tc.want, next.protocolMode)
			other, err := svc.getClientEntry("", 311, 1, service.HTTPUpstreamProfileOpenAI, false, false)
			require.NoError(t, err)
			require.Equal(t, upstreamProtocolModeOpenAIH2, other.protocolMode)
			if tc.want == upstreamProtocolModeOpenAIH1Fallback {
				state := svc.getOrCreateOpenAIHTTP2FallbackState(openAIHTTP2FallbackKey(directProxyKey, 310))
				state.mu.Lock()
				state.fallbackUntil = time.Now().Add(-time.Second)
				state.mu.Unlock()
				recovered, err := svc.getClientEntry("", 310, 1, service.HTTPUpstreamProfileOpenAI, false, false)
				require.NoError(t, err)
				require.Equal(t, upstreamProtocolModeOpenAIH2, recovered.protocolMode)
			}
		})
	}
}

func TestHTTPUpstreamStreamFailuresAccumulateAfterHeaders(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIHTTP2 = config.GatewayOpenAIHTTP2Config{Enabled: true, AllowProxyFallbackToHTTP1: true, FallbackErrorThreshold: 2}
	svc := NewHTTPUpstream(cfg).(*httpUpstreamService)
	proxy := "http://proxy.example:8080"
	entry, err := svc.getClientEntry(proxy, 310, 1, service.HTTPUpstreamProfileOpenAI, false, false)
	require.NoError(t, err)
	entry.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: &failingHTTP2Stream{err: errors.New("http2: client connection lost")}}, nil
	})
	for i := 0; i < 2; i++ {
		req, err := http.NewRequestWithContext(service.WithHTTPUpstreamProfile(t.Context(), service.HTTPUpstreamProfileOpenAI), http.MethodPost, "https://provider.example/v1/chat/completions", strings.NewReader("{}"))
		require.NoError(t, err)
		resp, err := svc.Do(req, proxy, 310, 1)
		require.NoError(t, err)
		_, err = io.ReadAll(resp.Body)
		require.Error(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, i == 1, svc.isOpenAIHTTP2FallbackActive(proxy))
	}
}
