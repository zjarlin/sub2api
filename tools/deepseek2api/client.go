package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultAPIBase = "https://chat.deepseek.com/api/v0"
	defaultWasmURL = "https://fe-static.deepseek.com/chat/static/sha3_wasm_bg.7b9ca65ddd.wasm"
	completionPath = "/api/v0/chat/completion"
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
	Token    string `json:"token"`
	DeviceID string `json:"device_id"`
	UID      string `json:"uid"`
	Email    string `json:"email,omitempty"`
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
	req.Header.Set("User-Agent", "DeepSeek/2.5.0 Android/35")
	req.Header.Set("X-Client-Version", "2.5.0")
	req.Header.Set("X-Client-Platform", "android")
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
	}
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("DeepSeek returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
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
	if envelope.Code != 0 || envelope.Data == nil {
		return fmt.Errorf("DeepSeek rejected the request, code %d", envelope.Code)
	}
	if envelope.Data.BizCode != 0 {
		return fmt.Errorf("DeepSeek rejected the request, business code %d", envelope.Data.BizCode)
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
			IsMuted bool `json:"is_muted"`
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

func (u *upstreamClient) completion(ctx context.Context, c webCredential, sessionID, prompt, model string, thinking bool, pow string) (*http.Response, error) {
	body := map[string]any{
		"chat_session_id":   sessionID,
		"parent_message_id": nil,
		"model_type":        model,
		"prompt":            prompt,
		"ref_file_ids":      []string{},
		"thinking_enabled":  thinking,
		"search_enabled":    false,
		"preempt":           false,
	}
	return u.request(ctx, c, http.MethodPost, "/chat/completion", body, pow)
}
