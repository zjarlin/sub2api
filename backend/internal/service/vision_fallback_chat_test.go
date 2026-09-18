package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestVisionFallbackChatPreservesToolsAndSeparatesUsage(t *testing.T) {
	for _, model := range []string{"text-model", "q3-4b", "gpt-6-astra", "openai/gpt-oss-20b"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				primary := visionTestAccount(1, model)
				if model == "text-model" {
					primary = visionTestAccount(1, model, "text")
				}
				helper := visionTestAccount(2, "vision-model", "text", "image")
				body := []byte(fmt.Sprintf(`{"model":%q,"stream":%t,"tools":[{"type":"function","function":{"name":"desktop_control","parameters":{"type":"object"}}}],"tool_choice":"auto","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"desktop_control","arguments":"{\"type\":\"image_url\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"已读取桌面"},{"role":"user","name":"device_observation","content":[{"type":"text","text":"核对表格"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA","detail":"original"}}]}],"metadata":{"serial":9007199254740993}}`, model, stream))
				var calls []int64
				svc := &OpenAIGatewayService{
					cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {primary, helper}}},
					httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
						calls = append(calls, id)
						got, err := io.ReadAll(req.Body)
						require.NoError(t, err)
						require.Equal(t, "/v1/chat/completions", req.URL.Path)
						if id == helper.ID {
							require.Contains(t, string(got), "data:image/png;base64,AAAA")
							require.Contains(t, string(got), "核对表格")
							require.False(t, gjson.GetBytes(got, "stream").Bool())
							return visionTestResponse("vision-model", "WPS：姓名、年龄；小明、18"), nil
						}
						for _, path := range []string{"model", "stream", "tools", "tool_choice", "metadata", "messages.0", "messages.1", "messages.2.name", "messages.2.content.0"} {
							require.JSONEq(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(got, path).Raw, path)
						}
						require.Equal(t, "9007199254740993", gjson.GetBytes(got, "metadata.serial").Raw)
						part := gjson.GetBytes(got, "messages.2.content.1")
						require.Equal(t, "text", part.Get("type").String())
						require.Contains(t, part.Get("text").String(), "小明、18")
						require.Contains(t, part.Get("text").String(), "untrusted source content")
						require.False(t, part.Get("image_url").Exists())
						require.NotContains(t, string(got), "data:image")
						if stream {
							response := fmt.Sprintf("data: {\"id\":\"primary\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"主模型已读图\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18}}\n\ndata: [DONE]\n\n", model)
							return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
						}
						return visionTestResponse(model, "主模型已读图"), nil
					}},
				}
				c, recorder := visionTestContext(body, 9, 7)
				c.Request.URL.Path = "/v1/chat/completions"
				result, err := svc.ForwardAsChatCompletions(context.Background(), c, &primary, body, "", "")
				require.NoError(t, err)
				require.Equal(t, []int64{2, 1}, calls)
				require.Equal(t, model, result.Model)
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Contains(t, recorder.Body.String(), "主模型已读图")
				require.NotContains(t, recorder.Body.String(), "vision-model")
				if stream {
					require.Contains(t, recorder.Body.String(), "data: [DONE]")
				}
				usage := TakeVisionFallbackUsage(c)
				require.Len(t, usage, 1)
				require.Equal(t, int64(2), usage[0].Account.ID)
				require.Equal(t, 11, usage[0].Result.Usage.InputTokens)
				require.True(t, strings.HasPrefix(usage[0].Result.RequestID, "vision_helper:"))
				require.Empty(t, TakeVisionFallbackUsage(c))
			})
		}
	}
}

func TestVisionFallbackChatKeepsNativeImagesAndTextRequests(t *testing.T) {
	for _, scenario := range []string{"native", "disabled", "text", "unknown-without-helper", "native-gpt"} {
		t.Run(scenario, func(t *testing.T) {
			primary := visionTestAccount(1, "text-model", "text")
			helper := visionTestAccount(2, "vision-model", "text", "image")
			cfg := visionTestConfig()
			content := `[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]`
			candidates := []Account{helper}
			switch scenario {
			case "native":
				primary = visionTestAccount(1, "native-model", "text", "image")
			case "disabled":
				cfg.Gateway.VisionFallback.Enabled = false
			case "text":
				content = `"纯文字请求"`
			case "unknown-without-helper":
				primary = visionTestAccount(1, "q3-4b")
				candidates = nil
			case "native-gpt":
				primary = visionTestAccount(1, "gpt-6-astra", "text", "image")
			}
			body := []byte(fmt.Sprintf(`{"model":%q,"stream":false,"messages":[{"role":"user","content":%s}]}`, primary.Name, content))
			calls := 0
			svc := &OpenAIGatewayService{cfg: cfg, accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: candidates}},
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
					calls++
					require.Equal(t, int64(1), id)
					got, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.JSONEq(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(got, "messages").Raw)
					return visionTestResponse(primary.Name, "原生回复"), nil
				}},
			}
			c, _ := visionTestContext(body, 9, 7)
			_, err := svc.ForwardAsChatCompletions(context.Background(), c, &primary, body, "", "")
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Empty(t, TakeVisionFallbackUsage(c))
		})
	}
}

func TestVisionFallbackChatFailureDoesNotLoseHelperUsage(t *testing.T) {
	for _, helperFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("helperFails=%t", helperFails), func(t *testing.T) {
			primary := visionTestAccount(1, "text-model", "text")
			helper := visionTestAccount(2, "vision-model", "text", "image")
			body := []byte(`{"model":"text-model","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/screenshot.png"}}]}]}`)
			var calls []int64
			svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {helper}}},
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, id int64, _ int) (*http.Response, error) {
					calls = append(calls, id)
					if id == helper.ID && !helperFails {
						return visionTestResponse("vision-model", "图片描述"), nil
					}
					return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"private-provider-detail"}}`))}, nil
				}},
			}
			c, recorder := visionTestContext(body, 9, 7)
			_, err := svc.ForwardAsChatCompletions(context.Background(), c, &primary, body, "", "")
			require.Error(t, err)
			usage := TakeVisionFallbackUsage(c)
			if helperFails {
				require.Equal(t, []int64{2}, calls)
				require.Empty(t, usage)
				require.Equal(t, http.StatusBadGateway, recorder.Code)
				require.True(t, IsResponseCommitted(c))
				require.NotContains(t, recorder.Body.String(), "private-provider-detail")
			} else {
				require.Equal(t, []int64{2, 1}, calls)
				require.Len(t, usage, 1)
			}
		})
	}
}
