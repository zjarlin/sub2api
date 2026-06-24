package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	openAIVideoStorageObjectContentType = "video/mp4"
	openAIVideoStorageLifecycleRuleID   = "sub2api-video-expiration"
)

type openAIVideoStorageRef struct {
	Bucket string `json:"bucket"`
	Key    string `json:"key"`
	URL    string `json:"url"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func (s *OpenAIGatewayService) videoStorageEnabled() bool {
	if s == nil || s.cfg == nil {
		return false
	}
	cfg := s.cfg.Gateway.VideoStorage
	return cfg.Enabled &&
		strings.TrimSpace(cfg.Endpoint) != "" &&
		strings.TrimSpace(cfg.Bucket) != "" &&
		strings.TrimSpace(cfg.AccessKeyID) != "" &&
		strings.TrimSpace(cfg.SecretAccessKey) != "" &&
		strings.TrimSpace(cfg.PublicBaseURL) != ""
}

func (s *OpenAIGatewayService) enrichAgnesAIVideoResponseBody(
	ctx context.Context,
	account *Account,
	body []byte,
	requestModel string,
	upstreamModel string,
	token string,
) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) || !s.videoStorageEnabled() {
		return body
	}
	ref := s.localAgnesAIVideoStorageRef(body, requestModel)
	if ref.URL == "" {
		return body
	}

	out := body
	out, _ = sjson.SetBytes(out, "local_url", ref.URL)
	out, _ = sjson.SetBytes(out, "url", ref.URL)
	out, _ = sjson.SetRawBytes(out, "storage", mustJSONRaw(ref))

	status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String()))
	upstreamURL := strings.TrimSpace(gjson.GetBytes(body, "remixed_from_video_id").String())
	switch {
	case status == "completed" && upstreamURL != "":
		s.startAgnesAIVideoStorageUpload(ctx, account, body, requestModel, upstreamURL)
	case status == "queued" || status == "in_progress" || status == "processing" || status == "":
		s.startAgnesAIVideoStoragePoller(account, body, requestModel, upstreamModel, token)
	}
	return out
}

func (s *OpenAIGatewayService) enrichAgnesAIResponsesVideoBody(ctx context.Context, account *Account, upstreamBody []byte, responseBody []byte, requestModel string, upstreamModel string, token string) []byte {
	if len(responseBody) == 0 || !gjson.ValidBytes(responseBody) || !s.videoStorageEnabled() {
		return responseBody
	}
	ref := s.localAgnesAIVideoStorageRef(upstreamBody, requestModel)
	if ref.URL == "" {
		return responseBody
	}
	out := responseBody
	out, _ = sjson.SetBytes(out, "output.0.result", ref.URL)
	out, _ = sjson.SetBytes(out, "output.0.local_url", ref.URL)
	out, _ = sjson.SetRawBytes(out, "output.0.storage", mustJSONRaw(ref))

	status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(upstreamBody, "status").String()))
	upstreamURL := strings.TrimSpace(gjson.GetBytes(upstreamBody, "remixed_from_video_id").String())
	switch {
	case status == "completed" && upstreamURL != "":
		s.startAgnesAIVideoStorageUpload(ctx, account, upstreamBody, requestModel, upstreamURL)
	case status == "queued" || status == "in_progress" || status == "processing" || status == "":
		s.startAgnesAIVideoStoragePoller(account, upstreamBody, requestModel, upstreamModel, token)
	}
	return out
}

func (s *OpenAIGatewayService) localAgnesAIVideoStorageRef(body []byte, model string) openAIVideoStorageRef {
	if !s.videoStorageEnabled() {
		return openAIVideoStorageRef{}
	}
	keyID := canonicalAgnesAIVideoStorageID(body)
	if strings.TrimSpace(keyID) == "" {
		return openAIVideoStorageRef{}
	}
	key := s.openAIVideoStorageObjectKey(model, keyID)
	status := "uploading"
	if _, ok := s.videoStorageUploaded.Load(key); ok {
		status = "available"
	}
	return openAIVideoStorageRef{
		Bucket: strings.TrimSpace(s.cfg.Gateway.VideoStorage.Bucket),
		Key:    key,
		URL:    s.openAIVideoStoragePublicURL(key),
		Status: status,
	}
}

func (s *OpenAIGatewayService) openAIVideoStorageObjectKey(model string, id string) string {
	cfg := s.cfg.Gateway.VideoStorage
	prefix := strings.Trim(strings.TrimSpace(cfg.Prefix), "/")
	model = sanitizeVideoStoragePathSegment(model)
	if model == "" {
		model = "unknown-model"
	}
	id = sanitizeVideoStoragePathSegment(id)
	if id == "" {
		sum := sha256.Sum256([]byte(time.Now().String()))
		id = hex.EncodeToString(sum[:8])
	}
	parts := []string{}
	if prefix != "" {
		parts = append(parts, prefix)
	}
	parts = append(parts, model, time.Now().UTC().Format("2006/01/02"), id+".mp4")
	return path.Join(parts...)
}

func (s *OpenAIGatewayService) openAIVideoStoragePublicURL(key string) string {
	base := strings.TrimRight(strings.TrimSpace(s.cfg.Gateway.VideoStorage.PublicBaseURL), "/")
	if base == "" || key == "" {
		return ""
	}
	return base + "/" + strings.TrimLeft(key, "/")
}

func sanitizeVideoStoragePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if len(out) > 160 {
		out = out[:160]
	}
	return out
}

func (s *OpenAIGatewayService) startAgnesAIVideoStoragePoller(account *Account, body []byte, requestModel string, upstreamModel string, token string) {
	if !s.videoStorageEnabled() || account == nil {
		return
	}
	videoID := firstNonEmptyJSONString(body, "video_id", "data.video_id")
	taskID := firstNonEmptyJSONString(body, "task_id", "id", "data.task_id", "data.id")
	queryID := videoID
	if queryID == "" {
		queryID = taskID
	}
	if strings.TrimSpace(queryID) == "" {
		return
	}
	ref := s.localAgnesAIVideoStorageRef(body, requestModel)
	if ref.URL == "" {
		return
	}
	uploadKey := "poll|" + ref.Key
	if _, loaded := s.videoStorageUploads.LoadOrStore(uploadKey, struct{}{}); loaded {
		return
	}
	go func() {
		defer s.videoStorageUploads.Delete(uploadKey)
		s.pollAndUploadAgnesAIVideo(account, queryID, requestModel, upstreamModel, token)
	}()
}

func (s *OpenAIGatewayService) startAgnesAIVideoStorageUpload(ctx context.Context, account *Account, body []byte, requestModel string, upstreamURL string) {
	if !s.videoStorageEnabled() || strings.TrimSpace(upstreamURL) == "" {
		return
	}
	ref := s.localAgnesAIVideoStorageRef(body, requestModel)
	if ref.Key == "" {
		return
	}
	uploadKey := "upload|" + ref.Key
	if _, loaded := s.videoStorageUploads.LoadOrStore(uploadKey, struct{}{}); loaded {
		return
	}
	go func() {
		defer s.videoStorageUploads.Delete(uploadKey)
		bg := context.Background()
		if ctx != nil {
			if deadline, ok := ctx.Deadline(); ok {
				var cancel context.CancelFunc
				bg, cancel = context.WithDeadline(bg, deadline)
				defer cancel()
			}
		}
		if err := s.uploadOpenAIVideoURLToStorage(bg, upstreamURL, ref.Key); err != nil {
			slog.Warn("openai video storage upload failed",
				"account_id", accountIDForLog(account),
				"key", ref.Key,
				"error", sanitizeUpstreamErrorMessage(err.Error()),
			)
		}
	}()
}

func (s *OpenAIGatewayService) pollAndUploadAgnesAIVideo(account *Account, queryID string, requestModel string, upstreamModel string, token string) {
	cfg := s.effectiveVideoStorageConfig()
	interval := time.Duration(cfg.PollIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	timeout := time.Duration(cfg.PollTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	modelName := strings.TrimSpace(upstreamModel)
	if modelName == "" {
		modelName = strings.TrimSpace(requestModel)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		body, err := s.queryAgnesAIVideoStatusForStorage(ctx, account, queryID, modelName, token)
		if err == nil && len(body) > 0 && gjson.ValidBytes(body) {
			status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String()))
			if status == "completed" {
				upstreamURL := strings.TrimSpace(gjson.GetBytes(body, "remixed_from_video_id").String())
				ref := s.localAgnesAIVideoStorageRef(body, requestModel)
				if upstreamURL != "" && ref.Key != "" {
					if err := s.uploadOpenAIVideoURLToStorage(ctx, upstreamURL, ref.Key); err != nil {
						slog.Warn("openai video storage upload failed",
							"account_id", accountIDForLog(account),
							"key", ref.Key,
							"error", sanitizeUpstreamErrorMessage(err.Error()),
						)
					}
				}
				return
			}
			if status == "failed" || status == "cancelled" || status == "canceled" {
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *OpenAIGatewayService) queryAgnesAIVideoStatusForStorage(ctx context.Context, account *Account, queryID string, modelName string, token string) ([]byte, error) {
	if account == nil {
		return nil, fmt.Errorf("missing account")
	}
	targetURL := buildOpenAIVideosTaskURL(account.GetOpenAIBaseURL(), queryID, modelName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	if strings.TrimSpace(token) == "" {
		token, _, err = s.GetAccessToken(ctx, account)
		if err != nil {
			return nil, err
		}
	}
	applyOpenAIUpstreamAuthHeaders(req.Header, account, token)
	resp, err := s.httpUpstream.Do(req, "", account.ID, account.Concurrency)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("query Agnes video status: %d %s", resp.StatusCode, truncateString(string(body), 512))
	}
	return body, nil
}

func (s *OpenAIGatewayService) uploadOpenAIVideoURLToStorage(ctx context.Context, upstreamURL string, key string) error {
	if !s.videoStorageEnabled() {
		return nil
	}
	cfg := s.effectiveVideoStorageConfig()
	timeout := time.Duration(cfg.UploadTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	uploadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := validateOpenAIVideoStorageSourceURL(upstreamURL); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(uploadCtx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download video: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("download video returned %d", resp.StatusCode)
	}
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 512 * 1024 * 1024
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read video: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("video exceeds max size %d bytes", maxBytes)
	}
	client, err := s.newOpenAIVideoStorageS3Client(uploadCtx, cfg)
	if err != nil {
		return err
	}
	if err := s.ensureOpenAIVideoStorageBucket(uploadCtx, client, cfg); err != nil {
		return err
	}
	input := &s3.PutObjectInput{
		Bucket:      aws.String(cfg.Bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(openAIVideoStorageObjectContentType),
	}
	if cfg.PublicRead {
		input.ACL = types.ObjectCannedACLPublicRead
	}
	_, err = client.PutObject(uploadCtx, input)
	if err != nil && cfg.PublicRead {
		input.ACL = ""
		_, err = client.PutObject(uploadCtx, input)
	}
	if err != nil {
		return fmt.Errorf("put video object: %w", err)
	}
	s.videoStorageUploaded.Store(key, struct{}{})
	return nil
}

func validateOpenAIVideoStorageSourceURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid video source url")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("unsupported video source url scheme")
	}
	return nil
}

func (s *OpenAIGatewayService) effectiveVideoStorageConfig() config.GatewayVideoStorageConfig {
	if s == nil || s.cfg == nil {
		return config.GatewayVideoStorageConfig{}
	}
	cfg := s.cfg.Gateway.VideoStorage
	if strings.TrimSpace(cfg.Region) == "" {
		cfg.Region = "us-east-1"
	}
	if strings.TrimSpace(cfg.Prefix) == "" {
		cfg.Prefix = "videos/"
	}
	if cfg.PollIntervalSeconds <= 0 {
		cfg.PollIntervalSeconds = 5
	}
	if cfg.PollTimeoutSeconds <= 0 {
		cfg.PollTimeoutSeconds = 600
	}
	if cfg.UploadTimeoutSeconds <= 0 {
		cfg.UploadTimeoutSeconds = 300
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 512 * 1024 * 1024
	}
	return cfg
}

func (s *OpenAIGatewayService) newOpenAIVideoStorageS3Client(ctx context.Context, cfg config.GatewayVideoStorageConfig) (*s3.Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("load video storage aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		endpoint := strings.TrimSpace(cfg.Endpoint)
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = cfg.ForcePathStyle
		o.APIOptions = append(o.APIOptions, v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	return client, nil
}

func (s *OpenAIGatewayService) ensureOpenAIVideoStorageBucket(ctx context.Context, client *s3.Client, cfg config.GatewayVideoStorageConfig) error {
	if client == nil {
		return fmt.Errorf("missing video storage client")
	}
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return fmt.Errorf("missing video storage bucket")
	}
	bucketCacheKey := "bucket|" + bucket
	if _, loaded := s.videoStorageBucketEnsured.LoadOrStore(bucketCacheKey, struct{}{}); !loaded {
		if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err != nil {
			_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
			if err != nil {
				s.videoStorageBucketEnsured.Delete(bucketCacheKey)
				return fmt.Errorf("create video storage bucket: %w", err)
			}
		}
	}

	if err := s.ensureOpenAIVideoStorageLifecycle(ctx, client, cfg); err != nil {
		slog.Warn("openai video storage lifecycle setup failed",
			"bucket", bucket,
			"expiration_days", cfg.ExpirationDays,
			"error", sanitizeUpstreamErrorMessage(err.Error()),
		)
	}
	return nil
}

func (s *OpenAIGatewayService) ensureOpenAIVideoStorageLifecycle(ctx context.Context, client *s3.Client, cfg config.GatewayVideoStorageConfig) error {
	if client == nil {
		return fmt.Errorf("missing video storage client")
	}
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return fmt.Errorf("missing video storage bucket")
	}
	if cfg.ExpirationDays <= 0 {
		return nil
	}
	prefix := openAIVideoStorageLifecyclePrefix(cfg.Prefix)
	cacheKey := fmt.Sprintf("lifecycle|%s|%s|%d", bucket, prefix, cfg.ExpirationDays)
	if _, loaded := s.videoStorageBucketEnsured.LoadOrStore(cacheKey, struct{}{}); loaded {
		return nil
	}

	existingRules := []types.LifecycleRule{}
	current, err := client.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{
		Bucket: aws.String(bucket),
	})
	if err == nil && current != nil {
		existingRules = current.Rules
	} else if err != nil && !isOpenAIVideoStorageNoLifecycleError(err) {
		s.videoStorageBucketEnsured.Delete(cacheKey)
		return fmt.Errorf("get video storage lifecycle: %w", err)
	}

	lifecycle, err := buildOpenAIVideoStorageLifecycleConfiguration(existingRules, cfg)
	if err != nil {
		s.videoStorageBucketEnsured.Delete(cacheKey)
		return err
	}
	if lifecycle == nil {
		return nil
	}
	_, err = client.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
		Bucket:                 aws.String(bucket),
		LifecycleConfiguration: lifecycle,
	})
	if err != nil {
		s.videoStorageBucketEnsured.Delete(cacheKey)
		return fmt.Errorf("put video storage lifecycle: %w", err)
	}
	return nil
}

func buildOpenAIVideoStorageLifecycleConfiguration(existingRules []types.LifecycleRule, cfg config.GatewayVideoStorageConfig) (*types.BucketLifecycleConfiguration, error) {
	if cfg.ExpirationDays <= 0 {
		return nil, nil
	}
	if cfg.ExpirationDays > 36500 {
		return nil, fmt.Errorf("video storage expiration_days too large: %d", cfg.ExpirationDays)
	}
	days := int32(cfg.ExpirationDays)
	rule := types.LifecycleRule{
		ID:     aws.String(openAIVideoStorageLifecycleRuleID),
		Status: types.ExpirationStatusEnabled,
		Filter: &types.LifecycleRuleFilter{
			Prefix: aws.String(openAIVideoStorageLifecyclePrefix(cfg.Prefix)),
		},
		Expiration: &types.LifecycleExpiration{
			Days: aws.Int32(days),
		},
		AbortIncompleteMultipartUpload: &types.AbortIncompleteMultipartUpload{
			DaysAfterInitiation: aws.Int32(days),
		},
	}

	rules := make([]types.LifecycleRule, 0, len(existingRules)+1)
	replaced := false
	for _, existing := range existingRules {
		if strings.TrimSpace(aws.ToString(existing.ID)) == openAIVideoStorageLifecycleRuleID {
			if !replaced {
				rules = append(rules, rule)
				replaced = true
			}
			continue
		}
		rules = append(rules, existing)
	}
	if !replaced {
		rules = append(rules, rule)
	}
	return &types.BucketLifecycleConfiguration{Rules: rules}, nil
}

func openAIVideoStorageLifecyclePrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return ""
	}
	return prefix + "/"
}

func isOpenAIVideoStorageNoLifecycleError(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "NoSuchLifecycleConfiguration") ||
		strings.Contains(text, "The lifecycle configuration does not exist") ||
		strings.Contains(text, "status code: 404") ||
		strings.Contains(text, "StatusCode: 404")
}

func firstNonEmptyJSONString(body []byte, paths ...string) string {
	for _, path := range paths {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			return value
		}
	}
	return ""
}

func canonicalAgnesAIVideoStorageID(body []byte) string {
	for _, candidate := range []string{
		firstNonEmptyJSONString(body, "video_id", "data.video_id"),
		firstNonEmptyJSONString(body, "id", "data.id"),
		firstNonEmptyJSONString(body, "task_id", "data.task_id"),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if decoded := decodeAgnesAIVideoWrappedID(candidate); decoded != "" {
			return decoded
		}
		if strings.HasPrefix(candidate, "video_") {
			return candidate
		}
		if strings.HasPrefix(candidate, "task_") {
			return candidate
		}
	}
	return ""
}

func decodeAgnesAIVideoWrappedID(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "video_") || len(value) <= len("video_") {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "video_"))
	if err != nil {
		return ""
	}
	text := string(raw)
	marker := "video_id:"
	idx := strings.LastIndex(text, marker)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(marker):]
	if semi := strings.Index(rest, ";"); semi >= 0 {
		rest = rest[:semi]
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "video_") {
		return rest
	}
	return ""
}

func mustJSONRaw(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

func accountIDForLog(account *Account) int64 {
	if account == nil {
		return 0
	}
	return account.ID
}
