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
	openAIImageStorageLifecycleRuleID   = "sub2api-image-expiration"
)

type openAIVideoStorageRef struct {
	Bucket           string `json:"bucket"`
	Key              string `json:"key"`
	URL              string `json:"url"`
	Status           string `json:"status"`
	MediaType        string `json:"media_type,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	ExpiresInSeconds int64  `json:"expires_in_seconds,omitempty"`
	Error            string `json:"error,omitempty"`
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
	out = appendAgnesAIResponsesJSONMessage(out, "agnes_video_result", map[string]any{
		"type":    "agnes_video_result",
		"model":   strings.TrimSpace(requestModel),
		"status":  strings.TrimSpace(gjson.GetBytes(upstreamBody, "status").String()),
		"url":     ref.URL,
		"storage": ref,
	})

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

func (s *OpenAIGatewayService) enrichAgnesAIResponsesImageBody(ctx context.Context, upstreamBody []byte, responseBody []byte, requestModel string) []byte {
	if len(responseBody) == 0 || !gjson.ValidBytes(responseBody) || !s.videoStorageEnabled() {
		return responseBody
	}
	data := gjson.GetBytes(upstreamBody, "data")
	if !data.IsArray() {
		return responseBody
	}
	out := responseBody
	images := make([]map[string]any, 0, len(data.Array()))
	outputIndex := 0
	data.ForEach(func(_, item gjson.Result) bool {
		if strings.TrimSpace(item.Get("b64_json").String()) == "" && strings.TrimSpace(item.Get("url").String()) == "" {
			return true
		}
		result := map[string]any{
			"index": outputIndex,
		}
		if revised := strings.TrimSpace(item.Get("revised_prompt").String()); revised != "" {
			result["revised_prompt"] = revised
		}
		if size := strings.TrimSpace(item.Get("size").String()); size != "" {
			result["size"] = size
		}
		ref, format, err := s.storeAgnesAIResponseImageItem(ctx, item, requestModel, outputIndex)
		if err == nil && ref.URL != "" {
			out, _ = sjson.SetBytes(out, fmt.Sprintf("output.%d.result", outputIndex), ref.URL)
			out, _ = sjson.SetBytes(out, fmt.Sprintf("output.%d.local_url", outputIndex), ref.URL)
			out, _ = sjson.SetBytes(out, fmt.Sprintf("output.%d.output_format", outputIndex), format)
			out, _ = sjson.SetRawBytes(out, fmt.Sprintf("output.%d.storage", outputIndex), mustJSONRaw(ref))
			result["url"] = ref.URL
			result["output_format"] = format
			result["storage"] = ref
		} else {
			if upstreamURL := strings.TrimSpace(item.Get("url").String()); upstreamURL != "" {
				result["upstream_url"] = upstreamURL
			}
			if err != nil {
				result["storage_error"] = sanitizeUpstreamErrorMessage(err.Error())
			}
		}
		images = append(images, result)
		outputIndex++
		return true
	})
	if len(images) == 0 {
		return out
	}
	out = appendAgnesAIResponsesJSONMessage(out, "agnes_image_result", map[string]any{
		"type":   "agnes_image_result",
		"model":  strings.TrimSpace(requestModel),
		"images": images,
	})
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
	expiresIn := s.openAIVideoStorageURLTTL()
	expiresAt := time.Now().UTC().Add(expiresIn)
	return openAIVideoStorageRef{
		Bucket:           strings.TrimSpace(s.cfg.Gateway.VideoStorage.Bucket),
		Key:              key,
		URL:              s.openAIMediaStorageURL(key, expiresIn),
		Status:           status,
		MediaType:        "video",
		ExpiresAt:        expiresAt.Format(time.RFC3339),
		ExpiresInSeconds: int64(expiresIn.Seconds()),
	}
}

func (s *OpenAIGatewayService) openAIVideoStorageObjectKey(model string, id string) string {
	cfg := s.effectiveVideoStorageConfig()
	return openAIMediaStorageObjectKey(cfg.Prefix, model, id, "mp4")
}

func (s *OpenAIGatewayService) openAIImageStorageObjectKey(model string, id string, extension string) string {
	cfg := s.effectiveVideoStorageConfig()
	if extension == "" {
		extension = "png"
	}
	return openAIMediaStorageObjectKey(cfg.ImagePrefix, model, id, extension)
}

func openAIMediaStorageObjectKey(prefix string, model string, id string, extension string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	model = sanitizeVideoStoragePathSegment(model)
	if model == "" {
		model = "unknown-model"
	}
	id = sanitizeVideoStoragePathSegment(id)
	if id == "" {
		sum := sha256.Sum256([]byte(time.Now().String()))
		id = hex.EncodeToString(sum[:8])
	}
	extension = sanitizeVideoStoragePathSegment(strings.TrimPrefix(extension, "."))
	if extension == "" {
		extension = "bin"
	}
	parts := []string{}
	if prefix != "" {
		parts = append(parts, prefix)
	}
	parts = append(parts, model, time.Now().UTC().Format("2006/01/02"), id+"."+extension)
	return path.Join(parts...)
}

func (s *OpenAIGatewayService) openAIVideoStoragePublicURL(key string) string {
	base := strings.TrimRight(strings.TrimSpace(s.cfg.Gateway.VideoStorage.PublicBaseURL), "/")
	if base == "" || key == "" {
		return ""
	}
	return base + "/" + strings.TrimLeft(key, "/")
}

func (s *OpenAIGatewayService) openAIMediaStorageURL(key string, ttlOverride time.Duration) string {
	ttl := s.openAIVideoStorageURLTTL()
	if ttlOverride > 0 {
		ttl = ttlOverride
	}
	if url, err := s.presignOpenAIMediaStorageURL(context.Background(), key, ttl); err == nil && strings.TrimSpace(url) != "" {
		return url
	}
	return s.openAIVideoStoragePublicURL(key)
}

func (s *OpenAIGatewayService) presignOpenAIMediaStorageURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if !s.videoStorageEnabled() {
		return "", fmt.Errorf("media storage disabled")
	}
	key = strings.TrimLeft(strings.TrimSpace(key), "/")
	if key == "" {
		return "", fmt.Errorf("missing media storage key")
	}
	if ttl <= 0 {
		return "", fmt.Errorf("missing media storage url ttl")
	}
	maxTTL := 7 * 24 * time.Hour
	if ttl > maxTTL {
		ttl = maxTTL
	}
	cfg := s.effectiveVideoStorageConfig()
	client, err := s.newOpenAIMediaStoragePresignS3Client(ctx, cfg)
	if err != nil {
		return "", err
	}
	presigner := s3.NewPresignClient(client)
	result, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign media object: %w", err)
	}
	return result.URL, nil
}

func (s *OpenAIGatewayService) newOpenAIMediaStoragePresignS3Client(ctx context.Context, cfg config.GatewayVideoStorageConfig) (*s3.Client, error) {
	presignCfg := cfg
	if endpoint := openAIMediaStoragePresignEndpoint(cfg); endpoint != "" {
		presignCfg.Endpoint = endpoint
	}
	return s.newOpenAIVideoStorageS3Client(ctx, presignCfg)
}

func openAIMediaStoragePresignEndpoint(cfg config.GatewayVideoStorageConfig) string {
	raw := strings.TrimSpace(cfg.PublicBaseURL)
	if raw == "" {
		return strings.TrimSpace(cfg.Endpoint)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimSpace(cfg.Endpoint)
	}
	return parsed.Scheme + "://" + parsed.Host
}

func (s *OpenAIGatewayService) openAIVideoStorageURLTTL() time.Duration {
	cfg := s.effectiveVideoStorageConfig()
	days := cfg.ExpirationDays
	if days <= 0 {
		days = 7
	}
	return time.Duration(days) * 24 * time.Hour
}

func (s *OpenAIGatewayService) openAIImageStorageURLTTL() time.Duration {
	cfg := s.effectiveVideoStorageConfig()
	days := cfg.ImageExpirationDays
	if days <= 0 {
		days = 3
	}
	return time.Duration(days) * 24 * time.Hour
}

func (s *OpenAIGatewayService) localOpenAIMediaStorageRef(key string, mediaType string, ttl time.Duration, status string) openAIVideoStorageRef {
	if key == "" {
		return openAIVideoStorageRef{}
	}
	expiresAt := time.Now().UTC().Add(ttl)
	return openAIVideoStorageRef{
		Bucket:           strings.TrimSpace(s.cfg.Gateway.VideoStorage.Bucket),
		Key:              key,
		URL:              s.openAIMediaStorageURL(key, ttl),
		Status:           status,
		MediaType:        mediaType,
		ExpiresAt:        expiresAt.Format(time.RFC3339),
		ExpiresInSeconds: int64(ttl.Seconds()),
	}
}

func (s *OpenAIGatewayService) storeAgnesAIResponseImageItem(ctx context.Context, item gjson.Result, model string, outputIndex int) (openAIVideoStorageRef, string, error) {
	b64 := strings.TrimSpace(item.Get("b64_json").String())
	if b64 != "" {
		data, declaredContentType, err := decodeAgnesAIImageBase64(b64)
		if err != nil {
			return openAIVideoStorageRef{}, "", err
		}
		format, contentType := detectAgnesAIImageFormat(data, declaredContentType)
		keyID := hashOpenAIImageOutputResult(fmt.Sprintf("%d:%s", outputIndex, b64))
		if len(keyID) > 32 {
			keyID = keyID[:32]
		}
		key := s.openAIImageStorageObjectKey(model, keyID, format)
		if err := s.uploadOpenAIMediaBytesToStorage(ctx, data, key, contentType); err != nil {
			return openAIVideoStorageRef{}, "", err
		}
		return s.localOpenAIMediaStorageRef(key, "image", s.openAIImageStorageURLTTL(), "available"), format, nil
	}

	upstreamURL := strings.TrimSpace(item.Get("url").String())
	if upstreamURL == "" {
		return openAIVideoStorageRef{}, "", fmt.Errorf("Agnes image output has no b64_json or url")
	}
	keyID := hashOpenAIImageOutputResult(fmt.Sprintf("%d:%s", outputIndex, upstreamURL))
	if len(keyID) > 32 {
		keyID = keyID[:32]
	}
	key := s.openAIImageStorageObjectKey(model, keyID, "png")
	format, err := s.uploadOpenAIMediaURLToStorage(ctx, upstreamURL, key, "image")
	if err != nil {
		return openAIVideoStorageRef{}, "", err
	}
	return s.localOpenAIMediaStorageRef(key, "image", s.openAIImageStorageURLTTL(), "available"), format, nil
}

func decodeAgnesAIImageBase64(value string) ([]byte, string, error) {
	value = strings.TrimSpace(value)
	contentType := ""
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		comma := strings.Index(value, ",")
		if comma < 0 {
			return nil, "", fmt.Errorf("invalid data url image")
		}
		meta := value[len("data:"):comma]
		if semi := strings.Index(meta, ";"); semi >= 0 {
			contentType = strings.TrimSpace(meta[:semi])
		} else {
			contentType = strings.TrimSpace(meta)
		}
		value = value[comma+1:]
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil {
		return nil, "", fmt.Errorf("decode image base64: %w", err)
	}
	return data, contentType, nil
}

func detectAgnesAIImageFormat(data []byte, declaredContentType string) (string, string) {
	contentType := strings.ToLower(strings.TrimSpace(declaredContentType))
	switch {
	case strings.Contains(contentType, "image/jpeg") || strings.Contains(contentType, "image/jpg"):
		return "jpg", "image/jpeg"
	case strings.Contains(contentType, "image/png"):
		return "png", "image/png"
	case strings.Contains(contentType, "image/webp"):
		return "webp", "image/webp"
	case strings.Contains(contentType, "image/gif"):
		return "gif", "image/gif"
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "webp", "image/webp"
	}
	if len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4e && data[3] == 0x47 {
		return "png", "image/png"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "jpg", "image/jpeg"
	}
	if len(data) >= 3 && string(data[0:3]) == "GIF" {
		return "gif", "image/gif"
	}
	return "png", "image/png"
}

func (s *OpenAIGatewayService) uploadOpenAIMediaBytesToStorage(ctx context.Context, data []byte, key string, contentType string) error {
	if !s.videoStorageEnabled() {
		return nil
	}
	if len(data) == 0 {
		return fmt.Errorf("empty media data")
	}
	cfg := s.effectiveVideoStorageConfig()
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 512 * 1024 * 1024
	}
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("media exceeds max size %d bytes", maxBytes)
	}
	timeout := time.Duration(cfg.UploadTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	uploadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := s.newOpenAIVideoStorageS3Client(uploadCtx, cfg)
	if err != nil {
		return err
	}
	if err := s.ensureOpenAIVideoStorageBucket(uploadCtx, client, cfg); err != nil {
		return err
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	input := &s3.PutObjectInput{
		Bucket:      aws.String(cfg.Bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
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
		return fmt.Errorf("put media object: %w", err)
	}
	s.videoStorageUploaded.Store(key, struct{}{})
	return nil
}

func (s *OpenAIGatewayService) uploadOpenAIMediaURLToStorage(ctx context.Context, upstreamURL string, key string, fallbackMediaType string) (string, error) {
	if err := validateOpenAIVideoStorageSourceURL(upstreamURL); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download media: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("download media returned %d", resp.StatusCode)
	}
	cfg := s.effectiveVideoStorageConfig()
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 512 * 1024 * 1024
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read media: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return "", fmt.Errorf("media exceeds max size %d bytes", maxBytes)
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	format, contentType := detectAgnesAIImageFormat(data, contentType)
	if fallbackMediaType == "video" {
		format = "mp4"
		contentType = openAIVideoStorageObjectContentType
	}
	return format, s.uploadOpenAIMediaBytesToStorage(ctx, data, key, contentType)
}

func appendAgnesAIResponsesJSONMessage(response []byte, messageIDPrefix string, value any) []byte {
	if len(response) == 0 || !gjson.ValidBytes(response) {
		return response
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return response
	}
	idHash := hashOpenAIImageOutputResult(string(raw))
	if len(idHash) > 24 {
		idHash = idHash[:24]
	}
	message := map[string]any{
		"id":     fmt.Sprintf("msg_%s_%s", sanitizeVideoStoragePathSegment(messageIDPrefix), idHash),
		"type":   "message",
		"status": "completed",
		"role":   "assistant",
		"content": []map[string]any{
			{
				"type": "output_text",
				"text": string(raw),
			},
		},
	}
	messageRaw, err := json.Marshal(message)
	if err != nil {
		return response
	}
	out := response
	out, _ = sjson.SetRawBytes(out, "output.-1", messageRaw)
	return out
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
	if strings.TrimSpace(cfg.ImagePrefix) == "" {
		cfg.ImagePrefix = "images/"
	}
	if cfg.ImageExpirationDays <= 0 {
		cfg.ImageExpirationDays = 3
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
			"image_expiration_days", cfg.ImageExpirationDays,
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
	videoPrefix := openAIVideoStorageLifecyclePrefix(cfg.Prefix)
	imagePrefix := openAIVideoStorageLifecyclePrefix(cfg.ImagePrefix)
	imageDays := cfg.ImageExpirationDays
	if imageDays <= 0 {
		imageDays = 3
	}
	cacheKey := fmt.Sprintf("lifecycle|%s|%s|%d|%s|%d", bucket, videoPrefix, cfg.ExpirationDays, imagePrefix, imageDays)
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
	managedRules := make([]types.LifecycleRule, 0, 2)
	if cfg.ExpirationDays > 0 {
		rule, err := buildOpenAIMediaStorageLifecycleRule(openAIVideoStorageLifecycleRuleID, cfg.Prefix, cfg.ExpirationDays, "video storage expiration_days")
		if err != nil {
			return nil, err
		}
		managedRules = append(managedRules, rule)
	}
	imageDays := cfg.ImageExpirationDays
	if imageDays <= 0 {
		imageDays = 3
	}
	imagePrefix := cfg.ImagePrefix
	if strings.TrimSpace(imagePrefix) == "" {
		imagePrefix = "images/"
	}
	if imageDays > 0 {
		rule, err := buildOpenAIMediaStorageLifecycleRule(openAIImageStorageLifecycleRuleID, imagePrefix, imageDays, "image storage expiration_days")
		if err != nil {
			return nil, err
		}
		managedRules = append(managedRules, rule)
	}
	if len(managedRules) == 0 {
		return nil, nil
	}

	rules := make([]types.LifecycleRule, 0, len(existingRules)+len(managedRules))
	for _, existing := range existingRules {
		switch strings.TrimSpace(aws.ToString(existing.ID)) {
		case openAIVideoStorageLifecycleRuleID, openAIImageStorageLifecycleRuleID:
			continue
		default:
			rules = append(rules, existing)
		}
	}
	rules = append(rules, managedRules...)
	return &types.BucketLifecycleConfiguration{Rules: rules}, nil
}

func buildOpenAIMediaStorageLifecycleRule(ruleID string, prefix string, days int, fieldName string) (types.LifecycleRule, error) {
	if days <= 0 {
		return types.LifecycleRule{}, fmt.Errorf("%s must be positive", fieldName)
	}
	if days > 36500 {
		return types.LifecycleRule{}, fmt.Errorf("%s too large: %d", fieldName, days)
	}
	ttlDays := int32(days)
	return types.LifecycleRule{
		ID:     aws.String(ruleID),
		Status: types.ExpirationStatusEnabled,
		Filter: &types.LifecycleRuleFilter{
			Prefix: aws.String(openAIVideoStorageLifecyclePrefix(prefix)),
		},
		Expiration: &types.LifecycleExpiration{
			Days: aws.Int32(ttlDays),
		},
		AbortIncompleteMultipartUpload: &types.AbortIncompleteMultipartUpload{
			DaysAfterInitiation: aws.Int32(ttlDays),
		},
	}, nil
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
