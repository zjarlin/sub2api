package service

import (
	"bytes"
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
// Credits 为 nil 表示本次没能从上游取到精确余额（此时 BalanceError 说明原因），
// 绝不回退成账号池估算值冒充官方余额。
type BuiltinAdapterCheckinAccount struct {
	UID          string                        `json:"uid"`
	Nickname     string                        `json:"nickname,omitempty"`
	Credits      *float64                      `json:"credits"`
	BalanceError string                        `json:"balance_error,omitempty"`
	Checkin      *BuiltinAdapterCheckinRecord  `json:"checkin,omitempty"`
	Checkins     []BuiltinAdapterCheckinRecord `json:"checkins"`
}

// BuiltinAdapterCheckinOverview 是管理端账号页消费的积分与签到汇总。
type BuiltinAdapterCheckinOverview struct {
	Platform        string                         `json:"platform"`
	FetchedAt       int64                          `json:"fetched_at"`
	TotalCredits    float64                        `json:"total_credits"`
	Accounts        []BuiltinAdapterCheckinAccount `json:"accounts"`
	BindingRequired bool                           `json:"binding_required,omitempty"`
}

type builtinAdapterStatusResponse struct {
	Accounts []struct {
		UID          string                       `json:"uid"`
		Nickname     string                       `json:"nickname"`
		Credits      *float64                     `json:"credits"`
		BalanceError string                       `json:"balance_error"`
		Checkin      *BuiltinAdapterCheckinRecord `json:"checkin"`
	} `json:"accounts"`
}

type builtinAdapterCheckinsResponse struct {
	Checkins map[string][]BuiltinAdapterCheckinRecord `json:"checkins"`
}

// BuiltinAdapterCheckinOverviewForAccount 只返回当前路由账号绑定的上游账号。
// 缺少绑定时不猜选账号，也不向浏览器返回共享池中其他账号的数据。
func BuiltinAdapterCheckinOverviewForAccount(ctx context.Context, account *Account) (*BuiltinAdapterCheckinOverview, error) {
	uid := strings.TrimSpace(account.GetExtraString("builtin_adapter_uid"))
	if uid == "" {
		return nil, infraerrors.BadRequest("BUILTIN_ACCOUNT_UNBOUND", "未绑定上游账号，请由管理员关联已有登录账号，或重新授权后保存关联")
	}
	return builtinAdapterCheckinOverview(ctx, account.Platform, uid)
}

// BuiltinAdapterManualCheckinForAccount 立即为当前路由账号绑定的上游账号签到，
// 并回读最新积分与签到历史（面板一次请求即可刷新）。
// 未绑定时不猜选账号，也不允许把整个共享池一起签到。
func BuiltinAdapterManualCheckinForAccount(ctx context.Context, account *Account) (*BuiltinAdapterCheckinOverview, error) {
	uid := strings.TrimSpace(account.GetExtraString("builtin_adapter_uid"))
	if uid == "" {
		return nil, infraerrors.BadRequest("BUILTIN_ACCOUNT_UNBOUND", "未绑定上游账号，请先关联已有登录账号或重新授权")
	}
	return builtinAdapterManualCheckin(ctx, account.Platform, uid)
}

// BuiltinAdapterManualCheckinForAdminAccount 是管理端入口：未绑定时同样拒绝
// （与管理端只读概览不同——签到是写操作，不能落到共享池里的任意账号）。
func BuiltinAdapterManualCheckinForAdminAccount(ctx context.Context, account *Account) (*BuiltinAdapterCheckinOverview, error) {
	return BuiltinAdapterManualCheckinForAccount(ctx, account)
}

// BuiltinAdapterPlatformCheckin 对某个内置适配器平台的全部登录账号执行一次签到，
// 并回读最新积分与签到历史。仅管理端编排（普通用户只能签自己绑定的那一个账号）。
//
// 走 sidecar 的 POST /checkin（不带 uids = 全池），与定时签到共用同一把互斥锁。
func BuiltinAdapterPlatformCheckin(ctx context.Context, platform string) (*BuiltinAdapterCheckinOverview, error) {
	if platform != PlatformWorkbuddy && platform != PlatformTraework {
		return nil, infraerrors.BadRequest("BUILTIN_CHECKIN_UNSUPPORTED", "account platform does not support check-in")
	}
	base, key := builtinAdapterBaseURL(platform), builtinAdapterAPIKey(platform)
	if base == "" || key == "" {
		return nil, infraerrors.BadRequest("BUILTIN_ADAPTER_DISABLED", "built-in adapter is not enabled or configured")
	}
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")

	// 空 uids：签该 sidecar 账号池里的全部账号。串行 + 每号 token/余额往返，
	// 超时给足；真实耗时由 sidecar 内部按账号数决定。
	var result struct {
		Results []struct {
			UID    string `json:"uid"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"results"`
	}
	if err := doBuiltinAdapterJSON(ctx, http.MethodPost, base+"/checkin", key,
		builtinAdapterCheckinRequest{}, &result, 10*time.Minute); err != nil {
		return nil, err
	}
	return BuiltinAdapterCheckinOverviewForPlatform(ctx, platform)
}

// 管理员可从已有登录账号中恢复旧记录的关联，不把账号池合计当成当前账号余额。
// 普通用户继续使用严格的单账号查询，不能借旧记录枚举其他人的账号。
func BuiltinAdapterCheckinOverviewForAdminAccount(ctx context.Context, account *Account) (*BuiltinAdapterCheckinOverview, error) {
	if strings.TrimSpace(account.GetExtraString("builtin_adapter_uid")) != "" {
		return BuiltinAdapterCheckinOverviewForAccount(ctx, account)
	}
	overview, err := BuiltinAdapterCheckinOverviewForPlatform(ctx, account.Platform)
	if err != nil {
		return nil, err
	}
	overview.BindingRequired = true
	overview.TotalCredits = 0
	return overview, nil
}

// BuiltinAdapterCheckinOverviewForPlatform 从 WorkBuddy/TRAE Work sidecar 拉取
// 脱敏状态与签到历史。目标地址和共享密钥只读取服务端配置，不接受浏览器输入。
func BuiltinAdapterCheckinOverviewForPlatform(ctx context.Context, platform string) (*BuiltinAdapterCheckinOverview, error) {
	return builtinAdapterCheckinOverview(ctx, platform, "")
}

func builtinAdapterCheckinOverview(ctx context.Context, platform, uid string) (*BuiltinAdapterCheckinOverview, error) {
	if platform != PlatformWorkbuddy && platform != PlatformTraework {
		return nil, infraerrors.BadRequest("BUILTIN_CHECKIN_UNSUPPORTED", "account platform does not support check-in history")
	}
	base, key := builtinAdapterBaseURL(platform), builtinAdapterAPIKey(platform)
	if base == "" || key == "" {
		return nil, infraerrors.BadRequest("BUILTIN_ADAPTER_DISABLED", "built-in adapter is not enabled or configured")
	}
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")

	statusURL := base + "/status"
	if platform == PlatformWorkbuddy {
		statusURL += "?refresh_credits=true"
	}
	if uid != "" {
		// 只查绑定的那个上游账号：避免签到后刷新时把所有账号的余额都重查一遍。
		sep := "?"
		if strings.Contains(statusURL, "?") {
			sep = "&"
		}
		statusURL += sep + "uid=" + url.QueryEscape(uid)
	}
	var status builtinAdapterStatusResponse
	if err := fetchBuiltinAdapterJSON(ctx, statusURL, key, &status); err != nil {
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
		if uid != "" && item.UID != uid {
			continue
		}
		// 单号余额取不到只影响该号的数字，签到历史与冷却原因照常展示。
		if item.Credits != nil {
			out.TotalCredits += *item.Credits
		}
		checkins := history.Checkins[item.UID]
		if checkins == nil {
			checkins = []BuiltinAdapterCheckinRecord{}
		}
		out.Accounts = append(out.Accounts, BuiltinAdapterCheckinAccount{
			UID: item.UID, Nickname: item.Nickname, Credits: item.Credits,
			BalanceError: item.BalanceError,
			Checkin:      item.Checkin, Checkins: checkins,
		})
	}
	if uid != "" && len(out.Accounts) == 0 {
		return nil, infraerrors.NotFound("BUILTIN_ACCOUNT_NOT_FOUND", "绑定的上游账号已不存在，请重新绑定")
	}
	return out, nil
}

// builtinAdapterCheckinRequest 是 sidecar POST /checkin 的入参。
type builtinAdapterCheckinRequest struct {
	UIDs []string `json:"uids"`
}

// builtinAdapterManualCheckin 触发 sidecar 对指定上游账号立即签到，
// 然后回读最新积分与签到历史。签到本身可能包含 token 刷新与上游往返，
// 所以写操作使用更长的超时；读操作仍保持 15s。
func builtinAdapterManualCheckin(ctx context.Context, platform, uid string) (*BuiltinAdapterCheckinOverview, error) {
	if platform != PlatformWorkbuddy && platform != PlatformTraework {
		return nil, infraerrors.BadRequest("BUILTIN_CHECKIN_UNSUPPORTED", "account platform does not support check-in")
	}
	base, key := builtinAdapterBaseURL(platform), builtinAdapterAPIKey(platform)
	if base == "" || key == "" {
		return nil, infraerrors.BadRequest("BUILTIN_ADAPTER_DISABLED", "built-in adapter is not enabled or configured")
	}
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")

	var result struct {
		Results []struct {
			UID    string `json:"uid"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"results"`
	}
	payload := builtinAdapterCheckinRequest{UIDs: []string{uid}}
	if err := doBuiltinAdapterJSON(ctx, http.MethodPost, base+"/checkin", key, payload, &result, 90*time.Second); err != nil {
		return nil, err
	}
	return builtinAdapterCheckinOverview(ctx, platform, uid)
}

func fetchBuiltinAdapterJSON(ctx context.Context, rawURL, key string, dst any) error {
	return doBuiltinAdapterJSON(ctx, http.MethodGet, rawURL, key, nil, dst, 15*time.Second)
}

func doBuiltinAdapterJSON(ctx context.Context, method, rawURL, key string, body any, dst any, timeout time.Duration) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return infraerrors.BadRequest("INVALID_ADAPTER_URL", "invalid built-in adapter URL")
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return infraerrors.BadRequest("INVALID_ADAPTER_REQUEST", "unable to encode built-in adapter request")
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), reader)
	if err != nil {
		return infraerrors.BadRequest("INVALID_ADAPTER_URL", "invalid built-in adapter URL")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	res, err := client.Do(req)
	if err != nil {
		return infraerrors.New(http.StatusBadGateway, "ADAPTER_UNREACHABLE", "unable to reach the built-in adapter")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusConflict {
		return infraerrors.New(http.StatusConflict, "BUILTIN_CHECKIN_BUSY", "上游签到正在进行中，请稍后刷新")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return infraerrors.New(http.StatusBadGateway, "ADAPTER_CHECKIN_FAILED", fmt.Sprintf("built-in adapter returned HTTP %d", res.StatusCode))
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, builtinAdapterCheckinMaxBody))
	if err := dec.Decode(dst); err != nil {
		return infraerrors.New(http.StatusBadGateway, "ADAPTER_CHECKIN_INVALID", "built-in adapter returned an invalid response")
	}
	return nil
}
