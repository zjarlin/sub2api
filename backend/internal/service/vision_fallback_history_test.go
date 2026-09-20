package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestVisionFallbackLongHistoryPreservesEveryImageAndReusesDescriptions(t *testing.T) {
	for _, kind := range []string{"message", "function_call_output", "custom_tool_call_output", "chat"} {
		t.Run(kind, func(t *testing.T) {
			primary := visionTestAccount(1, "deepseek-v4.1-flash", "text")
			helper := visionTestAccount(2, "vision-model", "text", "image")
			calls := 0
			svc := &OpenAIGatewayService{
				cfg:         visionTestConfig(),
				accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {helper}}},
				httpUpstream: &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
					require.Equal(t, helper.ID, id)
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					var imageURLs []string
					for _, message := range gjson.GetBytes(body, "messages").Array() {
						for _, part := range message.Get("content").Array() {
							if part.Get("type").String() == "image_url" {
								imageURLs = append(imageURLs, part.Get("image_url.url").String())
							}
						}
					}
					require.Len(t, imageURLs, 1)
					calls++
					return visionTestResponse("vision-model", "图片描述："+imageURLs[0]), nil
				}},
			}
			rootField, contentField, textType := "input", "content", "input_text"
			if kind == "chat" {
				rootField, textType = "messages", "text"
			} else if kind != "message" {
				contentField = "output"
			}
			// 重放十二张历史截图后再追加一张，只应为新增截图调用助手。
			for turn, count := range []int{12, 12, 13} {
				var items []any
				for index := 0; index < count; index++ {
					imageURL := fmt.Sprintf("https://example.com/screenshot-%02d.png", index)
					image := map[string]any{"type": "input_image", "image_url": imageURL}
					if kind == "chat" {
						image = map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}}
					}
					item := map[string]any{contentField: []any{
						map[string]any{"type": textType, "text": fmt.Sprintf("截图 %d", index)}, image,
					}}
					if contentField == "output" {
						item["type"], item["call_id"] = kind, fmt.Sprintf("call_%d", index)
					} else {
						item["role"] = "user"
					}
					items = append(items, item)
				}
				body, err := json.Marshal(map[string]any{"model": primary.Name, rootField: items})
				require.NoError(t, err)
				c, _ := visionTestContext(body, 9, 7)
				got, err := svc.prepareVisionFallback(context.Background(), c, &primary, body)
				require.NoError(t, err)
				require.Equal(t, count, calls)
				require.Equal(t, primary.Name, gjson.GetBytes(got, "model").String())
				updated := gjson.GetBytes(got, rootField).Array()
				require.Len(t, updated, count)
				for index, item := range updated {
					parts := item.Get(contentField).Array()
					require.Len(t, parts, 2)
					require.Equal(t, fmt.Sprintf("截图 %d", index), parts[0].Get("text").String())
					require.Equal(t, textType, parts[1].Get("type").String())
					require.Contains(t, parts[1].Get("text").String(), fmt.Sprintf("screenshot-%02d.png", index))
					require.False(t, parts[1].Get("image_url").Exists())
					if contentField == "output" {
						require.Equal(t, fmt.Sprintf("call_%d", index), item.Get("call_id").String())
						require.Equal(t, kind, item.Get("type").String())
					} else {
						require.Equal(t, "user", item.Get("role").String())
					}
				}
				require.Len(t, TakeVisionFallbackUsage(c), []int{12, 0, 1}[turn])
			}
		})
	}
}
