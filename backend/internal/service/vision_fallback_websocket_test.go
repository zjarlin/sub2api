package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestVisionFallbackWebSocketUnknownNonGPTFirstAndSubsequentTurns(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeHTTPBridge, OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			primary := visionTestAccount(1, "text-model")
			primary.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			helper := visionTestAccount(2, "vision-model", "text", "image")
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.VisionFallback.Enabled = true
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
			cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 5
			upstream := newStagedPassthroughConn()
			svc := newPassthroughLifecycleService(cfg, upstream)
			svc.accountRepo = codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {primary, helper}}}
			primaryRequests := make(chan []byte, 3)
			svc.httpUpstream = &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				if accountID == 2 {
					return visionTestResponse("vision-model", "helper-description"), nil
				}
				primaryRequests <- body
				event := `data: {"type":"response.completed","response":{"id":"resp-main","model":"text-model","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":1}}}` + "\n\n"
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(event))}, nil
			}}
			serverErr := make(chan error, 1)
			usageByTurn := make(chan []VisionFallbackUsage, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					serverErr <- err
					return
				}
				defer conn.CloseNow()
				_, first, err := conn.Read(r.Context())
				if err != nil {
					serverErr <- err
					return
				}
				c, _ := visionTestContext(first, 9, 7)
				c.Request = r.Clone(r.Context())
				hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, _ *OpenAIForwardResult, _ error) {
					usageByTurn <- TakeVisionFallbackUsage(c)
				}}
				serverErr <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, &primary, "test-key", first, hooks)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer client.CloseNow()
			for turn := 1; turn <= 2; turn++ {
				body := strings.Replace(visionTestInput, "{", `{"type":"response.create",`, 1)
				body = strings.Replace(body, `"stream":false`, `"stream":true`, 1)
				if turn == 2 {
					body = strings.Replace(body, `"model":"text-model",`, "", 1)
					body = strings.Replace(body, "AAAA", "BBBB", 1)
				}
				require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(body)))
				requests := primaryRequests
				if mode == OpenAIWSIngressModePassthrough {
					requests = upstream.writes
				}
				select {
				case request := <-requests:
					require.Contains(t, string(request), "helper-description")
					require.NotContains(t, string(request), "data:image")
					require.Equal(t, "text-model", gjson.GetBytes(request, "model").String())
				case err := <-serverErr:
					t.Fatalf("gateway stopped before forwarding: %v", err)
				case <-ctx.Done():
					t.Fatal("timed out waiting for transformed upstream request")
				}
				if mode == OpenAIWSIngressModePassthrough {
					upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp-%d","model":"text-model","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":1}}}`, turn))
				}
				_, event, err := client.Read(ctx)
				require.NoError(t, err)
				require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
				require.NotContains(t, string(event), "helper-description")
				select {
				case usage := <-usageByTurn:
					require.Len(t, usage, 1)
					require.Equal(t, int64(2), usage[0].Account.ID)
				case <-ctx.Done():
					t.Fatal("timed out waiting for auxiliary usage")
				}
			}
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err := <-serverErr:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal("timed out waiting for gateway completion")
			}
		})
	}
}
