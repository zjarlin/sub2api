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
				// 能力未知（无目录快照、也未做过读图探测）：公开别名映射到带地域前缀的模型。
				primary := visionTestAccount(844, "deepseek-v4.1-flash")
				primary.Platform = PlatformWorkbuddy
				primary.Credentials["model_mapping"] = map[string]any{"deepseek-v4.1-flash": "cn:deepseek-v4.1-flash"}
				primary.Extra = map[string]any{}
				helper := visionTestAccount(831, "vision-model", "text", "image")
				accounts := []Account{primary}
				if helperAvailable {
					accounts = append(accounts, helper)
				}
				var calls []int64
				svc := &OpenAIGatewayService{
					cfg: visionTestConfig(), accountRepo: &countingCodexModelsAccountRepo{accounts: accounts},
					httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
						calls = append(calls, id)
						body, err := io.ReadAll(req.Body)
						require.NoError(t, err)
						if id == helper.ID {
							require.Contains(t, string(body), "data:image/png;base64,AAAA")
							return visionTestResponse(helper.Name, "IMAGE_DESCRIPTION"), nil
						}
						require.Equal(t, "cn:deepseek-v4.1-flash", gjson.GetBytes(body, "model").String())
						if helperAvailable {
							require.Contains(t, string(body), "IMAGE_DESCRIPTION")
							require.NotContains(t, string(body), "data:image/")
						} else {
							// 能力未知且没有助手：保持“现状即证据”，原图照常转发，
							// 不能因为模型名像 DeepSeek 就判成纯文本并整轮失败。
							require.Contains(t, string(body), "data:image/png;base64,AAAA")
						}
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
				require.NoError(t, err)
				if !helperAvailable {
					require.Equal(t, []int64{primary.ID}, calls)
					require.Contains(t, recorder.Body.String(), "observed image")
					return
				}
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

// 模型名不是能力证据：DeepSeek 型号在不同上游的读图支持并不一致，只能由目录快照
// 或真实读图探测下结论；没有证据时保持 unknown，不得断言“纯文本”。
func TestVisionFallbackDeepSeekCapabilityRequiresEvidence(t *testing.T) {
	for _, model := range []string{"deepseek-v4.1-flash", "cn:deepseek-v4.1-flash", "global:deepseek-v4.1-flash"} {
		for _, platform := range []string{PlatformWorkbuddy, PlatformOpenAI} {
			t.Run(platform+"/"+model, func(t *testing.T) {
				unknown := visionTestAccount(844, model)
				unknown.Platform = platform
				unknown.Extra = map[string]any{}
				require.False(t, accountHasKnownTextOnlyInput(&unknown, model), "无证据时不能断言纯文本")
				require.False(t, accountHasNativeVision(&unknown, model))
				// 仍可借助组内视觉助手，能力由助手补齐。
				require.True(t, accountNeedsVisionFallback(&unknown, model))

				// 显式同步的能力优先，不受模型名影响。
				vision := visionTestAccount(844, model, "text", "image")
				vision.Platform = platform
				require.False(t, accountHasKnownTextOnlyInput(&vision, model))
				require.True(t, accountHasNativeVision(&vision, model))
				require.False(t, accountNeedsVisionFallback(&vision, model))

				// 上游明确声明纯文本（如 models.dev 快照）才是负面证据。
				textOnly := visionTestAccount(844, model, "text")
				textOnly.Platform = platform
				require.True(t, accountHasKnownTextOnlyInput(&textOnly, model))
				require.True(t, accountNeedsVisionFallback(&textOnly, model))
			})
		}
	}
}
