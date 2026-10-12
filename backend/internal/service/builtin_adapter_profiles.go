package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ListDoubaoSessionProfiles 从内置适配器读取可用的会话别名。
func ListDoubaoSessionProfiles(ctx context.Context) ([]string, error) {
	body, err := callDoubaoProfileEndpoint(ctx, http.MethodGet, "", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Profiles []string
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "DOUBAO_PROFILE_RESPONSE_INVALID", "doubao adapter returned an invalid profile list")
	}
	if len(result.Profiles) == 0 || result.Profiles[0] != "default" {
		return nil, infraerrors.New(http.StatusBadGateway, "DOUBAO_PROFILE_RESPONSE_INVALID", "doubao adapter returned an invalid profile list")
	}
	for _, profile := range result.Profiles {
		if profile != "default" && !doubaoSessionProfilePattern.MatchString(profile) {
			return nil, infraerrors.New(http.StatusBadGateway, "DOUBAO_PROFILE_RESPONSE_INVALID", "doubao adapter returned an invalid profile list")
		}
	}
	return result.Profiles, nil
}

// UploadDoubaoSessionProfile 将 Mac 端导出的豆包快照交给内置适配器保存。
func UploadDoubaoSessionProfile(ctx context.Context, profile string, snapshot []byte) error {
	if !doubaoSessionProfilePattern.MatchString(profile) || profile == "default" {
		return infraerrors.BadRequest("INVALID_DOUBAO_PROFILE", "Choose a valid Doubao profile name other than default")
	}
	if len(snapshot) == 0 || len(snapshot) > 64<<10 {
		return infraerrors.BadRequest("INVALID_DOUBAO_PROFILE", "Doubao session snapshot must be between 1 byte and 64 KiB")
	}
	_, err := callDoubaoProfileEndpoint(ctx, http.MethodPost, profile, snapshot)
	return err
}

// DeleteDoubaoSessionProfile 删除一个已上传的豆包会话快照。
func DeleteDoubaoSessionProfile(ctx context.Context, profile string) error {
	if !doubaoSessionProfilePattern.MatchString(profile) || profile == "default" {
		return infraerrors.BadRequest("INVALID_DOUBAO_PROFILE", "Choose a valid Doubao profile name other than default")
	}
	_, err := callDoubaoProfileEndpoint(ctx, http.MethodDelete, profile, nil)
	return err
}

func callDoubaoProfileEndpoint(ctx context.Context, method, profile string, body []byte) ([]byte, error) {
	base := builtinAdapterBaseURL(PlatformDoubao)
	key := builtinAdapterAPIKey(PlatformDoubao)
	if base == "" || key == "" {
		return nil, infraerrors.BadRequest("BUILTIN_ADAPTER_DISABLED", "Enable the built-in adapter and configure its shared key first")
	}
	endpoint := strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1") + "/internal/profiles"
	if profile != "" {
		endpoint += "/" + url.PathEscape(profile)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, infraerrors.New(http.StatusBadRequest, "INVALID_ADAPTER_URL", "Invalid adapter configuration")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "ADAPTER_UNREACHABLE", "Unable to reach the built-in Doubao adapter")
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "ADAPTER_RESPONSE_INVALID", "Unable to read the Doubao adapter response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		switch response.StatusCode {
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
			return nil, infraerrors.BadRequest("INVALID_DOUBAO_PROFILE", "Upload a valid Doubao desktop session snapshot (up to 64 KiB) and profile name")
		case http.StatusTooManyRequests:
			return nil, infraerrors.New(http.StatusTooManyRequests, "DOUBAO_PROFILE_BUSY", "Doubao adapter is busy; retry after the current request finishes")
		case http.StatusNotFound:
			return nil, infraerrors.New(http.StatusBadGateway, "DOUBAO_PROFILE_UNAVAILABLE", "The Doubao adapter does not support session management; update the adapter first")
		default:
			return nil, infraerrors.New(http.StatusBadGateway, "DOUBAO_PROFILE_REJECTED", "Unable to manage Doubao sessions; check the adapter configuration and writable storage")
		}
	}
	return responseBody, nil
}
