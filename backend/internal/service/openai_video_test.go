//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func openAIVideoTestAccount() *Account {
	return &Account{
		ID:       11,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":             "wuye-secret",
			"base_url":            "https://www.wuyeai.cn/v1",
			"openai_capabilities": []string{"video_generation"},
			"model_mapping":       map[string]any{"minimax-h3": "Minimax-H3-720p"},
		},
	}
}

func TestOpenAIVideoCreateMapsModelAndExposesBillingUnits(t *testing.T) {
	body := []byte(`{"model":"minimax-h3","prompt":"a cat in the rain","duration":8,"ratio":"16:9","resolution":"720p","referenceImages":["https://cdn.example/cat.png"],"future_field":{"keep":true}}`)
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"task_abc","object":"video","model":"Minimax-H3-720p","status":"queued","progress":0,"created_at":1}`)}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodPost, "/v1/videos", nil)

	result, err := svc.ForwardOpenAIVideo(context.Background(), c, openAIVideoTestAccount(), OpenAIVideoEndpointCreate, "", body, "application/json")
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"task_abc","object":"video","model":"Minimax-H3-720p","status":"queued","progress":0,"created_at":1}`, w.Body.String())
	require.Equal(t, "https://www.wuyeai.cn/v1/videos", upstream.request.URL.String())
	require.Equal(t, "Bearer wuye-secret", upstream.request.Header.Get("Authorization"))
	require.Equal(t, "openai-video:task_abc", result.ResponseID)
	require.Equal(t, 1, result.VideoCount)
	require.Equal(t, VideoBillingResolution720P, result.VideoResolution)
	require.Equal(t, 8, result.VideoDurationSeconds)
	require.Equal(t, "minimax-h3", result.Model)
	require.Equal(t, "Minimax-H3-720p", result.UpstreamModel)

	forwarded, err := io.ReadAll(upstream.request.Body)
	require.NoError(t, err)
	require.Equal(t, "Minimax-H3-720p", gjson.GetBytes(forwarded, "model").String())
	for _, field := range []string{"prompt", "duration", "ratio", "resolution", "referenceImages", "future_field"} {
		require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(forwarded, field).Raw, field)
	}
}

func TestOpenAIVideoStatusAndContentUsePrefixedTaskKey(t *testing.T) {
	statusBody := `{"id":"task_abc","object":"video","model":"Minimax-H3-720p","status":"completed","progress":100}`
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(statusBody)}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodGet, "/v1/videos/task_abc", nil)

	result, err := svc.ForwardOpenAIVideo(context.Background(), c, openAIVideoTestAccount(), OpenAIVideoEndpointStatus, OpenAIVideoTaskKey("task_abc"), nil, "")
	require.NoError(t, err)
	require.Equal(t, "/v1/videos/task_abc", upstream.request.URL.Path)
	require.Equal(t, "task_abc", result.ResponseID)
	require.Equal(t, "Minimax-H3-720p", result.Model)
	require.Zero(t, result.VideoCount, "status polling must not bill a second time")
	require.JSONEq(t, statusBody, w.Body.String())

	upstream = &grokMediaContentUpstreamStub{response: &http.Response{
		StatusCode:    http.StatusPartialContent,
		Header:        http.Header{"Content-Type": []string{"video/mp4"}, "Content-Length": []string{"13"}, "Content-Range": []string{"bytes 0-12/100"}, "Accept-Ranges": []string{"bytes"}},
		Body:          io.NopCloser(http.NoBody),
		ContentLength: 13,
	}}
	// Replace the empty body with the actual payload.
	upstream.response.Body = io.NopCloser(io.LimitReader(&stringReader{"video-payload"}, 13))
	svc = &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	c, w = grokMediaContentTestContext(http.MethodGet, "/v1/videos/task_abc/content", map[string]string{"Range": "bytes=0-12"})

	_, err = svc.ForwardOpenAIVideo(context.Background(), c, openAIVideoTestAccount(), OpenAIVideoEndpointContent, OpenAIVideoTaskKey("task_abc"), nil, "")
	require.NoError(t, err)
	require.Equal(t, "/v1/videos/task_abc/content", upstream.request.URL.Path)
	require.Equal(t, "bytes=0-12", upstream.request.Header.Get("Range"))
	require.Equal(t, http.StatusPartialContent, w.Code)
	require.Equal(t, "video/mp4", w.Header().Get("Content-Type"))
	require.Equal(t, "video-payload", w.Body.String())
}

// stringReader is a tiny io.Reader helper so the content test can keep the
// upstream stub's body construction explicit.
type stringReader struct{ value string }

func (r *stringReader) Read(p []byte) (int, error) {
	if r.value == "" {
		return 0, io.EOF
	}
	n := copy(p, r.value)
	r.value = r.value[n:]
	return n, nil
}

func TestOpenAIVideoValidationAndCapability(t *testing.T) {
	for _, body := range []string{``, `{`, `[]`, `{}`} {
		_, err := ParseOpenAIVideoRequest([]byte(body))
		require.Error(t, err, body)
	}
	info, err := ParseOpenAIVideoRequest([]byte(`{"model":"Minimax-H3-720p","prompt":"waves","seconds":6,"size":"720x1280","referenceImages":["https://cdn.example/a.png"]}`))
	require.NoError(t, err)
	require.Equal(t, 6, info.DurationSeconds)
	require.Equal(t, VideoBillingResolution720P, info.Resolution)
	require.Contains(t, string(info.ModerationBody()), "waves")
	require.Contains(t, string(info.ModerationBody()), "https://cdn.example/a.png")

	require.Equal(t, "https://www.wuyeai.cn/v1/videos", buildOpenAIVideoURLForTest(t, "https://www.wuyeai.cn", OpenAIVideoEndpointCreate, ""))
	require.Equal(t, "https://www.wuyeai.cn/v1/videos", buildOpenAIVideoURLForTest(t, "https://www.wuyeai.cn/v1", OpenAIVideoEndpointCreate, ""))
	require.Equal(t, "https://metaso.cn/api/openai/v1/videos", buildOpenAIVideoURLForTest(t, "https://metaso.cn/api/openai", OpenAIVideoEndpointCreate, ""))
	require.Equal(t, "https://www.wuyeai.cn/v1/videos/task_1", buildOpenAIVideoURLForTest(t, "https://www.wuyeai.cn/v1", OpenAIVideoEndpointStatus, OpenAIVideoTaskKey("task_1")))
	require.Equal(t, "https://www.wuyeai.cn/v1/videos/task_1/content", buildOpenAIVideoURLForTest(t, "https://www.wuyeai.cn/v1", OpenAIVideoEndpointContent, OpenAIVideoTaskKey("task_1")))

	for _, id := range []string{"", "..", "a/b", "a?b", "a#b"} {
		_, err := buildOpenAIVideoURL("https://www.wuyeai.cn/v1", OpenAIVideoEndpointStatus, id)
		require.Error(t, err, id)
	}

	account := openAIVideoTestAccount()
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityVideoGeneration))
	account.Type = AccountTypeOAuth
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityVideoGeneration))
	account.Type = AccountTypeAPIKey
	delete(account.Credentials, "openai_capabilities")
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityVideoGeneration))
}

func buildOpenAIVideoURLForTest(t *testing.T, base string, endpoint GrokMediaEndpoint, taskID string) string {
	t.Helper()
	got, err := buildOpenAIVideoURL(base, endpoint, taskID)
	require.NoError(t, err)
	return got
}
