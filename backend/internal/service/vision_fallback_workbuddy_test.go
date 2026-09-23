package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestVisionFallbackWorkbuddyForward(t *testing.T) {
	for _, protocol := range []string{"responses", "chat"} {
		for _, helperAvailable := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/helper=%t", protocol, helperAvailable), func(t *testing.T) {
				// 线上账号没有能力快照，公开别名映射到带地域前缀的模型。
				primary := visionTestAccount(844, "deepseek-v4.1-flash")
				primary.Platform = PlatformWorkbuddy
				primary.Credentials["model_mapping"] = map[string]any{"deepseek-v4.1-flash": "cn:deepseek-v4.1-flash"}
				helper := visionTestAccount(831, "vision-model", "text", "image")
				accounts := []Account{primary}
				if helperAvailable {
					accounts = append(accounts, helper)
				}
				var calls []int64
				svc := &OpenAIGatewayService{
					cfg: visionTestConfig(), accountRepo: &countingCodexModelsAccountRepo{accounts: accounts},
					httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
						require.True(t, helperAvailable, "助手不可用时不能把原始图片送入纯文本模型")
						calls = append(calls, id)
						body, err := io.ReadAll(req.Body)
						require.NoError(t, err)
						if id == helper.ID {
							require.Contains(t, string(body), "data:image/png;base64,AAAA")
							return visionTestResponse(helper.Name, "IMAGE_DESCRIPTION"), nil
						}
						require.Equal(t, "cn:deepseek-v4.1-flash", gjson.GetBytes(body, "model").String())
						require.Contains(t, string(body), "IMAGE_DESCRIPTION")
						require.NotContains(t, string(body), "data:image/")
						if protocol == "responses" {
							require.Contains(t, string(body), "call_image")
						}
						return visionTestResponse("cn:deepseek-v4.1-flash", "observed image"), nil
					}},
				}
				body := []byte(`{"model":"deepseek-v4.1-flash","stream":false,"input":[
					{"role":"user","content":"Inspect the image"},
					{"type":"function_call","call_id":"call_image","name":"view_image","arguments":"{}"},
					{"type":"function_call_output","call_id":"call_image","output":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}
				]}`)
				if protocol == "chat" {
					body = []byte(`{"model":"deepseek-v4.1-flash","stream":false,"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`)
				}
				c, recorder := visionTestContext(body, 9, 7)
				var err error
				if protocol == "responses" {
					_, err = svc.Forward(context.Background(), c, &primary, body)
				} else {
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, &primary, body, "", "")
				}
				if !helperAvailable {
					var failure *UpstreamFailoverError
					require.ErrorAs(t, err, &failure)
					require.Empty(t, calls)
					return
				}
				require.NoError(t, err)
				require.Equal(t, []int64{helper.ID, primary.ID}, calls)
				require.Contains(t, recorder.Body.String(), "observed image")
			})
		}
	}
}

func TestVisionFallbackWorkbuddyPreservesFailureAcrossAccountSwitch(t *testing.T) {
	primary := visionTestAccount(844, "text-model", "text")
	primary.Platform = PlatformWorkbuddy
	body := []byte(visionTestInput)
	c, _ := visionTestContext(body, 9, 7)
	svc := &OpenAIGatewayService{cfg: visionTestConfig(), accountRepo: &countingCodexModelsAccountRepo{}}
	state, err := svc.visionFallbackState(context.Background(), c)
	require.NoError(t, err)
	state.lastErr = newVisionFallbackFailoverError(http.StatusGatewayTimeout, "helper timed out")
	converted, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
	require.ErrorIs(t, err, state.lastErr)
	require.Nil(t, converted)
}

func TestVisionFallbackWorkbuddyModelCapabilities(t *testing.T) {
	for _, model := range []string{"deepseek-v4.1-flash", "cn:deepseek-v4.1-flash", "global:deepseek-v4.1-flash"} {
		t.Run(model, func(t *testing.T) {
			account := visionTestAccount(844, model)
			account.Platform = PlatformWorkbuddy
			require.True(t, accountHasKnownTextOnlyInput(&account, model))
			require.True(t, accountNeedsVisionFallback(&account, model))
			// 显式同步的能力仍然优先，不按名称覆盖供应商快照。
			account = visionTestAccount(844, model, "text", "image")
			account.Platform = PlatformWorkbuddy
			require.False(t, accountHasKnownTextOnlyInput(&account, model))
			require.False(t, accountNeedsVisionFallback(&account, model))
		})
	}
}
