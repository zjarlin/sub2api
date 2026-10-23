package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

const (
	defaultAPIBase   = "https://chat.deepseek.com/api/v0"
	defaultWasmURL   = "https://fe-static.deepseek.com/chat/static/sha3_wasm_bg.7b9ca65ddd.wasm"
	completionPath   = "/api/v0/chat/completion"
	defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

type upstreamClient struct {
	http    *http.Client
	baseURL string
}

type upstreamEnvelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data *struct {
		BizCode int             `json:"biz_code"`
		BizMsg  string          `json:"biz_msg"`
		BizData json.RawMessage `json:"biz_data"`
	} `json:"data"`
}

type webCredential struct {
	Token              string `json:"token"`
	DeviceID           string `json:"device_id"`
	UID                string `json:"uid"`
	Email              string `json:"email,omitempty"`
	UserAgent          string `json:"user_agent,omitempty"`
	ReloginCredentials string `json:"relogin_credentials,omitempty"`
}

var errDeepSeekUnauthorized = errors.New("DeepSeek browser session is no longer authorized")
var errDeepSeekPowRejected = errors.New("DeepSeek rejected the proof of work")

func (e upstreamEnvelope) responseError() error {
	if e.Code == 40301 || (e.Data != nil && e.Data.BizCode == 40301) {
		return errDeepSeekPowRejected
	}
	if e.Code == 40003 || (e.Data != nil && e.Data.BizCode == 40003) {
		return errDeepSeekUnauthorized
	}
	if e.Code != 0 {
		return fmt.Errorf("DeepSeek rejected the request, code %d", e.Code)
	}
	if e.Data != nil && e.Data.BizCode != 0 {
		return fmt.Errorf("DeepSeek rejected the request, business code %d", e.Data.BizCode)
	}
	return nil
}

type upstreamFlag bool

func (f *upstreamFlag) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "true", "1":
		*f = true
	case "false", "0", "null":
		*f = false
	default:
		return errors.New("DeepSeek returned an invalid boolean flag")
	}
	return nil
}

func (u *upstreamClient) request(ctx context.Context, credential webCredential, method, path string, body any, pow string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(u.baseURL, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+credential.Token)
	userAgent := credential.UserAgent
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	req.Header.Set("User-Agent", userAgent)
	origin := req.URL.Scheme + "://" + req.URL.Host
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	setClientHints(req.Header, userAgent)
	req.Header.Set("X-Client-Version", "2.5.0")
	req.Header.Set("X-Client-Platform", "web")
	req.Header.Set("X-Client-Locale", "zh_CN")
	req.Header.Set("X-Client-Bundle-Id", "com.deepseek.chat")
	req.Header.Set("X-Device-Id", credential.DeviceID)
	req.Header.Set("X-Device-Model", "")
	req.Header.Set("X-Client-Timezone-Offset", "28800")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if pow != "" {
		req.Header.Set("X-Ds-Pow-Response", pow)
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, errDeepSeekUnauthorized
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, apiError{429, "deepseek_rate_limited", "DeepSeek web is rate limited; retry later"}
		}
		var envelope upstreamEnvelope
		if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&envelope) == nil {
			if err := envelope.responseError(); err != nil {
				return nil, err
			}
		}
		return nil, fmt.Errorf("DeepSeek returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

var chromeVersion = regexp.MustCompile(`(?:Chrome|Chromium)/(\d+)`)

// 使用登录浏览器的身份生成一致的提示头，避免网页会话混用 Android 指纹。
func setClientHints(headers http.Header, userAgent string) {
	version := chromeVersion.FindStringSubmatch(userAgent)
	if len(version) != 2 {
		return
	}
	headers.Set("Sec-Ch-Ua", `"Chromium";v="`+version[1]+`", "Not_A Brand";v="24"`)
	headers.Set("Sec-Ch-Ua-Mobile", "?0")
	platform := "Linux"
	switch {
	case strings.Contains(userAgent, "Android"):
		platform = "Android"
		headers.Set("Sec-Ch-Ua-Mobile", "?1")
	case strings.Contains(userAgent, "Windows"):
		platform = "Windows"
	case strings.Contains(userAgent, "Mac OS X"):
		platform = "macOS"
	}
	headers.Set("Sec-Ch-Ua-Platform", `"`+platform+`"`)
}

func (u *upstreamClient) call(ctx context.Context, credential webCredential, method, path string, body any, out any) error {
	resp, err := u.request(ctx, credential, method, path, body, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var envelope upstreamEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode DeepSeek response: %w", err)
	}
	if err := envelope.responseError(); err != nil {
		return err
	}
	if envelope.Data == nil {
		return errors.New("DeepSeek response has no data")
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data.BizData) == 0 || string(envelope.Data.BizData) == "null" {
		return errors.New("DeepSeek response has no data")
	}
	return json.Unmarshal(envelope.Data.BizData, out)
}

func (u *upstreamClient) verify(ctx context.Context, c webCredential) (webCredential, error) {
	var user struct {
		ID    string `json:"id"`
		Token string `json:"token"`
		Email string `json:"email"`
		Chat  *struct {
			IsMuted upstreamFlag `json:"is_muted"`
		} `json:"chat"`
	}
	if err := u.call(ctx, c, http.MethodGet, "/users/current", nil, &user); err != nil {
		return c, err
	}
	if user.ID == "" || (user.Chat != nil && user.Chat.IsMuted) {
		return c, errors.New("DeepSeek account is unavailable")
	}
	c.UID, c.Email = user.ID, user.Email
	var device struct {
		Rotate json.RawMessage `json:"rotate"`
	}
	body := map[string]string{"device_id": c.DeviceID, "device_model": ""}
	if err := u.call(ctx, c, http.MethodPost, "/users/auth_token/check_device", body, &device); err != nil {
		return c, err
	}
	var rotated string
	if json.Unmarshal(device.Rotate, &rotated) != nil {
		var value struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(device.Rotate, &value)
		rotated = value.Token
	}
	if rotated != "" {
		c.Token = rotated
	}
	return c, nil
}

func (u *upstreamClient) createSession(ctx context.Context, c webCredential) (string, error) {
	var data struct {
		ChatSession struct {
			ID string `json:"id"`
		} `json:"chat_session"`
	}
	if err := u.call(ctx, c, http.MethodPost, "/chat_session/create", map[string]any{}, &data); err != nil {
		return "", err
	}
	if data.ChatSession.ID == "" {
		return "", errors.New("DeepSeek did not return a chat session ID")
	}
	return data.ChatSession.ID, nil
}

func (u *upstreamClient) deleteSession(ctx context.Context, c webCredential, id string) {
	_ = u.call(ctx, c, http.MethodPost, "/chat_session/delete", map[string]string{"chat_session_id": id}, nil)
}

func (u *upstreamClient) challenge(ctx context.Context, c webCredential) (challenge, error) {
	var data struct {
		Challenge challenge `json:"challenge"`
	}
	err := u.call(ctx, c, http.MethodPost, "/chat/create_pow_challenge", map[string]string{"target_path": completionPath}, &data)
	if err != nil {
		return challenge{}, err
	}
	if data.Challenge.TargetPath != completionPath {
		return challenge{}, errors.New("DeepSeek returned a PoW challenge for another path")
	}
	return data.Challenge, nil
}

func (u *upstreamClient) completion(ctx context.Context, c webCredential, sessionID, prompt, model string, thinking, search bool, pow string) (*http.Response, error) {
	body := map[string]any{
		"chat_session_id":   sessionID,
		"parent_message_id": nil,
		"model_type":        model,
		"prompt":            prompt,
		"ref_file_ids":      []string{},
		"thinking_enabled":  thinking,
		"search_enabled":    search,
		"stream":            true,
		"action":            nil,
		"preempt":           false,
	}
	return u.request(ctx, c, http.MethodPost, "/chat/completion", body, pow)
}
