package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const builtinAdapterCheckinMaxBody = 2 << 20

// BuiltinAdapterCheckinRecord 是内置适配器单次签到记录。
type BuiltinAdapterCheckinRecord struct {
	At      time.Time `json:"at"`
	Status  string    `json:"status"`
	Credits int64     `json:"credits"`
	Delta   int64     `json:"delta"`
	Detail  string    `json:"detail,omitempty"`
}

// BuiltinAdapterCheckinAccount 是内置适配器池中的单个上游账号。
type BuiltinAdapterCheckinAccount struct {
	UID      string                        `json:"uid"`
	Nickname string                        `json:"nickname,omitempty"`
	Credits  int64                         `json:"credits"`
	Checkin  *BuiltinAdapterCheckinRecord  `json:"checkin,omitempty"`
	Checkins []BuiltinAdapterCheckinRecord `json:"checkins"`
}

// BuiltinAdapterCheckinOverview 是管理端账号页消费的积分与签到汇总。
type BuiltinAdapterCheckinOverview struct {
	Platform     string                         `json:"platform"`
	FetchedAt    int64                          `json:"fetched_at"`
	TotalCredits int64                          `json:"total_credits"`
	Accounts     []BuiltinAdapterCheckinAccount `json:"accounts"`
}

type builtinAdapterStatusResponse struct {
	Accounts []struct {
		UID      string                       `json:"uid"`
		Nickname string                       `json:"nickname"`
		Credits  int64                        `json:"credits"`
		Checkin  *BuiltinAdapterCheckinRecord `json:"checkin"`
	} `json:"accounts"`
}

type builtinAdapterCheckinsResponse struct {
	Checkins map[string][]BuiltinAdapterCheckinRecord `json:"checkins"`
}

// BuiltinAdapterCheckinOverviewForPlatform 从 WorkBuddy/TRAE Work sidecar 拉取
// 脱敏状态与签到历史。目标地址和共享密钥只读取服务端配置，不接受浏览器输入。
func BuiltinAdapterCheckinOverviewForPlatform(ctx context.Context, platform string) (*BuiltinAdapterCheckinOverview, error) {
	if platform != PlatformWorkbuddy && platform != PlatformTraework {
		return nil, infraerrors.BadRequest("BUILTIN_CHECKIN_UNSUPPORTED", "account platform does not support check-in history")
	}
	base, key := builtinAdapterBaseURL(platform), builtinAdapterAPIKey(platform)
	if base == "" || key == "" {
		return nil, infraerrors.BadRequest("BUILTIN_ADAPTER_DISABLED", "built-in adapter is not enabled or configured")
	}
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")

	var status builtinAdapterStatusResponse
	if err := fetchBuiltinAdapterJSON(ctx, base+"/status", key, &status); err != nil {
		return nil, err
	}
	var history builtinAdapterCheckinsResponse
	if err := fetchBuiltinAdapterJSON(ctx, base+"/checkins", key, &history); err != nil {
		return nil, err
	}

	out := &BuiltinAdapterCheckinOverview{
		Platform:  platform,
		FetchedAt: time.Now().Unix(),
		Accounts:  make([]BuiltinAdapterCheckinAccount, 0, len(status.Accounts)),
	}
	for _, item := range status.Accounts {
		out.TotalCredits += item.Credits
		checkins := history.Checkins[item.UID]
		if checkins == nil {
			checkins = []BuiltinAdapterCheckinRecord{}
		}
		out.Accounts = append(out.Accounts, BuiltinAdapterCheckinAccount{
			UID: item.UID, Nickname: item.Nickname, Credits: item.Credits,
			Checkin: item.Checkin, Checkins: checkins,
		})
	}
	return out, nil
}

func fetchBuiltinAdapterJSON(ctx context.Context, rawURL, key string, dst any) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return infraerrors.BadRequest("INVALID_ADAPTER_URL", "invalid built-in adapter URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return infraerrors.BadRequest("INVALID_ADAPTER_URL", "invalid built-in adapter URL")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	res, err := client.Do(req)
	if err != nil {
		return infraerrors.New(http.StatusBadGateway, "ADAPTER_UNREACHABLE", "unable to reach the built-in adapter")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return infraerrors.New(http.StatusBadGateway, "ADAPTER_CHECKIN_FAILED", fmt.Sprintf("built-in adapter returned HTTP %d", res.StatusCode))
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, builtinAdapterCheckinMaxBody))
	if err := dec.Decode(dst); err != nil {
		return infraerrors.New(http.StatusBadGateway, "ADAPTER_CHECKIN_INVALID", "built-in adapter returned an invalid response")
	}
	return nil
}
