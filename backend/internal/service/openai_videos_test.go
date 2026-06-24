package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type recordingVideosHTTPUpstream struct {
	req  *http.Request
	body []byte
}

func (u *recordingVideosHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.req = req
	if req.Body != nil {
		u.body, _ = io.ReadAll(req.Body)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"x-request-id": []string{"req-video"},
		},
		Body: io.NopCloser(bytes.NewReader([]byte(`{"id":"cgt-test","status":"queued"}`))),
	}, nil
}

func (u *recordingVideosHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestOpenAIGatewayServiceParseOpenAIVideosRequest_Prompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"seedance-2.0","prompt":"a cat yawns","duration":5,"resolution":"720p","ratio":"16:9","stream":false}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := &OpenAIGatewayService{}
	parsed, err := svc.ParseOpenAIVideosRequest(c, body)
	require.NoError(t, err)
	require.Equal(t, "seedance-2.0", parsed.Model)
	require.Equal(t, "a cat yawns", parsed.Prompt)
	require.Equal(t, "720p", parsed.Resolution)
	require.Equal(t, "16:9", parsed.Ratio)
	require.False(t, parsed.Stream)
	contentJSON, err := json.Marshal(parsed.Content)
	require.NoError(t, err)
	require.JSONEq(t, `[{"type":"text","text":"a cat yawns"}]`, string(contentJSON))
}

func TestOpenAIGatewayServiceForwardVideos_RewritesPromptToSeedanceTask(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"seedance-2.0","prompt":"a cat yawns","duration":5,"stream":false}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	upstream := &recordingVideosHTTPUpstream{}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	parsed, err := svc.ParseOpenAIVideosRequest(c, body)
	require.NoError(t, err)
	account := &Account{
		ID:       7,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":       "ark-key",
			"base_url":      "https://ark.ap-southeast.bytepluses.com/api/v3",
			"model_mapping": map[string]any{"seedance-2.0": "seedance-2-0-pro-260128"},
		},
		Concurrency: 1,
	}

	result, err := svc.ForwardVideos(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "cgt-test", result.ResponseID)
	require.Equal(t, http.MethodPost, upstream.req.Method)
	require.Equal(t, "https://ark.ap-southeast.bytepluses.com/api/v3/contents/generations/tasks", upstream.req.URL.String())
	require.Equal(t, "Bearer ark-key", upstream.req.Header.Get("Authorization"))
	require.Equal(t, "seedance-2-0-pro-260128", gjson.GetBytes(upstream.body, "model").String())
	require.Equal(t, "a cat yawns", gjson.GetBytes(upstream.body, "content.0.text").String())
	require.False(t, gjson.GetBytes(upstream.body, "prompt").Exists())
	require.False(t, gjson.GetBytes(upstream.body, "stream").Exists())
}

func TestOpenAIGatewayServiceForwardVideos_RoutesAgnesToVideosEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"agnes-video-v2.0","prompt":"a red ball rolls","height":480,"width":854,"num_frames":25,"frame_rate":12,"stream":false}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	upstream := &recordingVideosHTTPUpstream{}
	upstream.body = nil
	upstream.req = nil
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	parsed, err := svc.ParseOpenAIVideosRequest(c, body)
	require.NoError(t, err)
	account := &Account{
		ID:       287,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "agnes-key",
			"base_url": "https://apihub.agnes-ai.com",
		},
		Concurrency: 1,
	}

	result, err := svc.ForwardVideos(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.req)
	require.Equal(t, http.MethodPost, upstream.req.Method)
	require.Equal(t, "https://apihub.agnes-ai.com/v1/videos", upstream.req.URL.String())
	require.Equal(t, "Bearer agnes-key", upstream.req.Header.Get("Authorization"))
	require.Equal(t, "agnes-video-v2.0", gjson.GetBytes(upstream.body, "model").String())
	require.Equal(t, "a red ball rolls", gjson.GetBytes(upstream.body, "prompt").String())
	require.Equal(t, int64(25), gjson.GetBytes(upstream.body, "num_frames").Int())
	require.False(t, gjson.GetBytes(upstream.body, "content").Exists())
	require.False(t, gjson.GetBytes(upstream.body, "stream").Exists())
}

func TestOpenAIGatewayServiceForwardVideoTask_RoutesAgnesVideoIDToAgnesAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/v1/videos/generations/video_1?model=agnes-video-v2.0", nil)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	upstream := &recordingVideosHTTPUpstream{}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:       287,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "agnes-key",
			"base_url": "https://apihub.agnes-ai.com",
			"model_mapping": map[string]any{
				"agnes-video-v2.0": "agnes-video-v2.0",
			},
		},
		Concurrency: 1,
	}

	result, err := svc.ForwardVideoTask(context.Background(), c, account, "video_1", "agnes-video-v2.0")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.req)
	require.Equal(t, http.MethodGet, upstream.req.Method)
	require.Equal(t, "https://apihub.agnes-ai.com/agnesapi?video_id=video_1&model_name=agnes-video-v2.0", upstream.req.URL.String())
	require.Equal(t, "Bearer agnes-key", upstream.req.Header.Get("Authorization"))
	require.Equal(t, "agnes-video-v2.0", result.Model)
	require.Equal(t, "agnes-video-v2.0", result.UpstreamModel)
}

func TestOpenAIGatewayServiceAgnesVideoStorageAddsPredictableLocalURL(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	svc.cfg.Gateway.VideoStorage.Enabled = true
	svc.cfg.Gateway.VideoStorage.Endpoint = "http://minio:9000"
	svc.cfg.Gateway.VideoStorage.PublicBaseURL = "http://localhost:19000/sub2api-videos"
	svc.cfg.Gateway.VideoStorage.Bucket = "sub2api-videos"
	svc.cfg.Gateway.VideoStorage.AccessKeyID = "minio"
	svc.cfg.Gateway.VideoStorage.SecretAccessKey = "secret"
	svc.cfg.Gateway.VideoStorage.Prefix = "videos"
	svc.cfg.Gateway.VideoStorage.ForcePathStyle = true

	wrapped := "video_" + base64.StdEncoding.EncodeToString([]byte("litellm:custom_llm_provider:openai;model_id:agnes-video-v2.0;video_id:video_final_123"))
	body := []byte(`{"id":"task_1","video_id":"` + wrapped + `","object":"video","model":"agnes-video-v2.0","status":"failed"}`)

	enriched := svc.enrichAgnesAIVideoResponseBody(context.Background(), &Account{}, body, "agnes-video-v2.0", "agnes-video-v2.0", "token")

	require.Equal(t, "http://localhost:19000/sub2api-videos/videos/agnes-video-v2.0/"+time.Now().UTC().Format("2006/01/02")+"/video_final_123.mp4", gjson.GetBytes(enriched, "local_url").String())
	require.Equal(t, gjson.GetBytes(enriched, "local_url").String(), gjson.GetBytes(enriched, "url").String())
	require.Equal(t, "sub2api-videos", gjson.GetBytes(enriched, "storage.bucket").String())
	require.Equal(t, "uploading", gjson.GetBytes(enriched, "storage.status").String())

	svc.videoStorageUploaded.Store("videos/agnes-video-v2.0/"+time.Now().UTC().Format("2006/01/02")+"/video_final_123.mp4", struct{}{})
	enriched = svc.enrichAgnesAIVideoResponseBody(context.Background(), &Account{}, body, "agnes-video-v2.0", "agnes-video-v2.0", "token")
	require.Equal(t, "available", gjson.GetBytes(enriched, "storage.status").String())
}

func TestBuildAgnesAIResponsesVideoResponseAddsLocalStorageResult(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	svc.cfg.Gateway.VideoStorage.Enabled = true
	svc.cfg.Gateway.VideoStorage.Endpoint = "http://minio:9000"
	svc.cfg.Gateway.VideoStorage.PublicBaseURL = "http://localhost:19000/sub2api-videos"
	svc.cfg.Gateway.VideoStorage.Bucket = "sub2api-videos"
	svc.cfg.Gateway.VideoStorage.AccessKeyID = "minio"
	svc.cfg.Gateway.VideoStorage.SecretAccessKey = "secret"
	svc.cfg.Gateway.VideoStorage.Prefix = "videos"
	svc.cfg.Gateway.VideoStorage.ForcePathStyle = true

	upstreamBody := []byte(`{"id":"task_1","video_id":"video_final_456","object":"video","model":"agnes-video-v2.0","status":"failed"}`)
	responseBody, _, _, err := buildAgnesAIResponsesVideoResponse(upstreamBody, "agnes-video-v2.0")
	require.NoError(t, err)

	enriched := svc.enrichAgnesAIResponsesVideoBody(context.Background(), &Account{}, upstreamBody, responseBody, "agnes-video-v2.0", "agnes-video-v2.0", "token")

	want := "http://localhost:19000/sub2api-videos/videos/agnes-video-v2.0/" + time.Now().UTC().Format("2006/01/02") + "/video_final_456.mp4"
	require.Equal(t, want, gjson.GetBytes(enriched, "output.0.result").String())
	require.Equal(t, want, gjson.GetBytes(enriched, "output.0.local_url").String())
	require.Equal(t, "videos/agnes-video-v2.0/"+time.Now().UTC().Format("2006/01/02")+"/video_final_456.mp4", gjson.GetBytes(enriched, "output.0.storage.key").String())
}

func TestBuildOpenAIVideoStorageLifecycleConfiguration(t *testing.T) {
	keepRule := types.LifecycleRule{
		ID:     aws.String("keep-existing"),
		Status: types.ExpirationStatusEnabled,
		Filter: &types.LifecycleRuleFilter{
			Prefix: aws.String("other/"),
		},
		Expiration: &types.LifecycleExpiration{
			Days: aws.Int32(30),
		},
	}
	oldRule := types.LifecycleRule{
		ID:     aws.String(openAIVideoStorageLifecycleRuleID),
		Status: types.ExpirationStatusEnabled,
		Filter: &types.LifecycleRuleFilter{
			Prefix: aws.String("videos/"),
		},
		Expiration: &types.LifecycleExpiration{
			Days: aws.Int32(1),
		},
	}

	lifecycle, err := buildOpenAIVideoStorageLifecycleConfiguration([]types.LifecycleRule{keepRule, oldRule}, config.GatewayVideoStorageConfig{
		Prefix:         "/videos/",
		ExpirationDays: 7,
	})
	require.NoError(t, err)
	require.NotNil(t, lifecycle)
	require.Len(t, lifecycle.Rules, 2)
	require.Equal(t, "keep-existing", aws.ToString(lifecycle.Rules[0].ID))

	rule := lifecycle.Rules[1]
	require.Equal(t, openAIVideoStorageLifecycleRuleID, aws.ToString(rule.ID))
	require.Equal(t, types.ExpirationStatusEnabled, rule.Status)
	require.NotNil(t, rule.Filter)
	require.Equal(t, "videos/", aws.ToString(rule.Filter.Prefix))
	require.NotNil(t, rule.Expiration)
	require.Equal(t, int32(7), aws.ToInt32(rule.Expiration.Days))
	require.NotNil(t, rule.AbortIncompleteMultipartUpload)
	require.Equal(t, int32(7), aws.ToInt32(rule.AbortIncompleteMultipartUpload.DaysAfterInitiation))
}

func TestBuildOpenAIVideosTaskURL(t *testing.T) {
	require.Equal(t,
		"https://ark.ap-southeast.bytepluses.com/api/v3/contents/generations/tasks/cgt-1",
		buildOpenAIVideosTaskURL("https://ark.ap-southeast.bytepluses.com/api/v3", "cgt-1"),
	)
	require.Equal(t,
		"https://ark.ap-southeast.bytepluses.com/api/v3/contents/generations/tasks/cgt-1",
		buildOpenAIVideosTaskURL("https://ark.ap-southeast.bytepluses.com/api/v3/contents/generations/tasks", "cgt-1"),
	)
	require.Equal(t,
		"https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks",
		buildOpenAIVideosTaskURL("https://ark.cn-beijing.volces.com/api/v3", ""),
	)
	require.Equal(t,
		"https://ark.cn-guangzhou.volces.com/api/v3/contents/generations/tasks/cgt-2",
		buildOpenAIVideosTaskURL("https://ark.cn-guangzhou.volces.com/api/v3", "cgt-2"),
	)
	require.Equal(t,
		"https://apihub.agnes-ai.com/v1/videos/task_1",
		buildOpenAIVideosTaskURL("https://apihub.agnes-ai.com", "task_1"),
	)
	require.Equal(t,
		"https://apihub.agnes-ai.com/agnesapi?video_id=video_1",
		buildOpenAIVideosTaskURL("https://apihub.agnes-ai.com", "video_1"),
	)
	require.Equal(t,
		"https://apihub.agnes-ai.com/agnesapi?video_id=video_1&model_name=agnes-video-v2.0",
		buildOpenAIVideosTaskURL("https://apihub.agnes-ai.com", "video_1", "agnes-video-v2.0"),
	)
}
