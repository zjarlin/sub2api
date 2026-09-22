package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestVisionFallbackToolImagesWithConfiguredHelper(t *testing.T) {
	for _, kind := range []string{"function", "custom_tool"} {
		for _, state := range []string{"available", "cooling_down", "rate_limited", "filtered_by_repository"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				primary := visionTestAccount(1, "text-model", "text")
				helper := visionTestAccount(2, "vision-model", "text", "image")
				until := time.Now().Add(time.Minute)
				switch state {
				case "cooling_down":
					helper.TempUnschedulableUntil = &until
				case "rate_limited":
					helper.RateLimitResetAt = &until
				}
				repo := &countingCodexModelsAccountRepo{accounts: []Account{primary, helper}}
				if state == "filtered_by_repository" {
					repo.accounts = []Account{primary}
				}
				var calls []int64
				svc := &OpenAIGatewayService{
					cfg: visionTestConfig(), accountRepo: repo,
					httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
						require.Equal(t, "available", state, "助手不可用时不能把删掉图片的请求交给主模型")
						calls = append(calls, accountID)
						body, err := io.ReadAll(req.Body)
						require.NoError(t, err)
						if accountID == helper.ID {
							require.Contains(t, string(body), "data:image/png;base64,AAAA")
							return visionTestResponse(helper.Name, "IMAGE_CONTENT_PRESERVED"), nil
						}
						require.Equal(t, primary.ID, accountID)
						require.Contains(t, string(body), "IMAGE_CONTENT_PRESERVED")
						require.Contains(t, string(body), "TEXT_RESULT_PRESERVED")
						require.Contains(t, string(body), "call_image")
						require.NotContains(t, string(body), "data:image")
						return visionTestResponse(primary.Name, "observed image"), nil
					}},
				}
				// 对应 view_image 的真实形态：只有图片的工具结果，旁边还有字符串工具结果。
				body := []byte(fmt.Sprintf(`{"model":"text-model","stream":false,"input":[
					{"role":"user","content":"Inspect the image"},
					{"type":"function_call","call_id":"call_text","name":"exec_command","arguments":"{}"},
					{"type":"function_call_output","call_id":"call_text","output":"TEXT_RESULT_PRESERVED"},
					{"type":"%s_call","call_id":"call_image","name":"view_image","arguments":"{}","input":"{}"},
					{"type":"%s_call_output","call_id":"call_image","output":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}
				]}`, kind, kind))
				c, recorder := visionTestContext(body, 9, 7)
				result, err := svc.Forward(context.Background(), c, &primary, body)
				if state == "available" {
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, []int64{helper.ID, primary.ID}, calls)
					require.Equal(t, "observed image", gjson.Get(recorder.Body.String(), "output.0.content.0.text").String())
					return
				}
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.Nil(t, result)
				require.True(t, failure.ShouldRetryNextAccount())
				require.False(t, failure.ShouldReportAccountScheduleFailure())
				require.Equal(t, http.StatusServiceUnavailable, failure.ClientStatusCode)
				require.Empty(t, calls)
				require.False(t, c.Writer.Written())
				require.Empty(t, recorder.Body.String())
				require.Empty(t, TakeVisionFallbackUsage(c))
			})
		}
	}
}
