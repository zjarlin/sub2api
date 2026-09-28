package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type upstreamError struct {
	status  int
	code    string
	message string
}

func (e *upstreamError) Error() string { return e.message }

func problem(status int, code, message string) error {
	return &upstreamError{status: status, code: code, message: message}
}

func (a *adapter) headers(c credential) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+c.Token)
	h.Set("RH-TOKEN", c.Token)
	h.Set("Cookie", "Rh-Accesstoken="+c.Token)
	h.Set("X-Tenant-Id", c.TenantID)
	h.Set("X-Vibex-Locale", "zh-CN")
	h.Set("userId", c.UID)
	h.Set("Origin", a.origin)
	return h
}

func (a *adapter) call(ctx context.Context, c credential, method, path string, body, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, a.origin+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header = a.headers(c)
	res, err := a.client.Do(req)
	if err != nil {
		return problem(502, "upstream_unavailable", "VibeX is unreachable")
	}
	defer res.Body.Close()
	data, err = io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return problem(502, "invalid_upstream_response", "Invalid VibeX response")
	}
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.Unmarshal(data, &envelope)
	if res.StatusCode == 401 || res.StatusCode == 403 || envelope.Msg == "TOKEN_MISSION" || envelope.Msg == "TOKEN_INVALID" || envelope.Code == 401 || envelope.Code == 403 || envelope.Code == 412 {
		return problem(401, "vibex_login_required", "RunningHub login expired; sign in again")
	}
	if res.StatusCode == 402 || bytes.Contains(data, []byte("BALANCE_INSUFFICIENT")) {
		return problem(402, "insufficient_balance", "VibeX wallet balance is insufficient")
	}
	if res.StatusCode == 429 || bytes.Contains(data, []byte("FREE_LLM_QUOTA_EXCEEDED")) || bytes.Contains(data, []byte("FREE_LLM_GLOBAL_BUDGET_EXCEEDED")) {
		return problem(429, "quota_exceeded", "VibeX quota is exhausted; retry after reset")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || envelope.Code != 0 {
		return problem(502, "upstream_error", "VibeX rejected the request")
	}
	if out != nil && json.Unmarshal(data, out) != nil {
		return problem(502, "invalid_upstream_response", "Invalid VibeX response")
	}
	return nil
}

type provider struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

func (a *adapter) providers(ctx context.Context, c credential) ([]provider, error) {
	var result struct {
		Providers []provider `json:"providers"`
	}
	if err := a.call(ctx, c, http.MethodGet, "/vc/api/llm-providers", nil, &result); err != nil {
		return nil, err
	}
	items := []provider{}
	seen := map[string]bool{}
	for _, p := range result.Providers {
		if strings.TrimSpace(p.ID) == "" || seen[p.ID] || (p.Enabled != nil && !*p.Enabled) {
			continue
		}
		seen[p.ID] = true
		items = append(items, p)
	}
	return items, nil
}
