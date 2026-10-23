package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifyAccountMutedFlag(t *testing.T) {
	cases := []struct {
		name    string
		flag    string
		wantErr string
	}{
		{name: "numeric available", flag: "0"},
		{name: "numeric muted", flag: "1", wantErr: "DeepSeek account is unavailable"},
		{name: "boolean available", flag: "false"},
		{name: "boolean muted", flag: "true", wantErr: "DeepSeek account is unavailable"},
		{name: "null available", flag: "null"},
		{name: "invalid number", flag: "2", wantErr: "invalid boolean flag"},
		{name: "invalid string", flag: `"0"`, wantErr: "invalid boolean flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deviceChecked := false
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/users/current":
					_, _ = fmt.Fprintf(w, `{"code":0,"data":{"biz_code":0,"biz_data":{"id":"user-1","email":"user@example.com","chat":{"is_muted":%s}}}}`, tc.flag)
				case "/users/auth_token/check_device":
					deviceChecked = true
					_, _ = fmt.Fprint(w, `{"code":0,"data":{"biz_code":0,"biz_data":{"rotate":null}}}`)
				default:
					t.Errorf("unexpected upstream path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()

			client := &upstreamClient{http: upstream.Client(), baseURL: upstream.URL}
			credential := webCredential{Token: "browser-token", DeviceID: "browser-device"}
			verified, err := client.verify(context.Background(), credential)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("verify error = %v, want %q", err, tc.wantErr)
				}
				if deviceChecked {
					t.Fatal("device checked for an unavailable or invalid account")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if verified.UID != "user-1" || verified.Email != "user@example.com" || verified.Token != credential.Token || verified.DeviceID != credential.DeviceID {
				t.Fatalf("unexpected verified credential: %#v", verified)
			}
			if !deviceChecked {
				t.Fatal("device was not checked")
			}
		})
	}
}

func TestBrowserIdentityAndRateLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Client-Platform") != "web" || r.Header.Get("User-Agent") != "Mozilla/5.0 (X11; Linux x86_64) Chrome/140.0.0.0" || !strings.Contains(r.Header.Get("Sec-Ch-Ua"), `v="140"`) {
			t.Error("browser identity is inconsistent")
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(w, `{"code":40003,"msg":"private diagnostic"}`)
	}))
	defer upstream.Close()
	client := &upstreamClient{http: upstream.Client(), baseURL: upstream.URL}
	_, err := client.request(context.Background(), webCredential{Token: "test", UserAgent: "Mozilla/5.0 (X11; Linux x86_64) Chrome/140.0.0.0"}, http.MethodGet, "/test", nil, "")
	api, ok := err.(apiError)
	if !ok || api.status != 429 || api.code != "deepseek_rate_limited" {
		t.Fatalf("rate limit was not preserved: %v", err)
	}
}
