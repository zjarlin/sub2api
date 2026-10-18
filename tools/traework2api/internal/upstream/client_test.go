package upstream

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"traework2api/internal/auth"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   ErrKind
	}{
		{200, `{"code":1005,"message":"plan limit","extra":{"plan":2}}`, ErrPlanLimit},
		{200, `{"code":1005,"msg":"权益不足"}`, ErrPlanLimit},
		{429, ``, ErrSoftRate},
		{401, `{"code":1001,"msg":"login required"}`, ErrSessionDead},
		{401, ``, ErrSessionDead},
		{404, ``, ErrNotFound},
		{500, `boom`, ErrServer},
		{503, `unavailable`, ErrServer},
		{400, `{"code":11101,"msg":"bad param"}`, ErrClient},
		{200, `{"checked_in":false}`, ErrNone},
	}
	for _, c := range cases {
		if got := Classify(c.status, c.body); got != c.want {
			t.Errorf("Classify(%d,%q)=%v want %v", c.status, c.body, got, c.want)
		}
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testClient(fn rtFunc) *Client {
	return &Client{
		HTTP:      &http.Client{Transport: fn},
		AgentHost: "https://agent.example",
		UgHost:    "https://ug.example",
		OAuthHost: "https://oauth.example",
		ClientID:  ClientID,
	}
}

func TestRefreshTokenExchange(t *testing.T) {
	c := testClient(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, EpExchange) {
			return nil, errors.New("wrong path: " + r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			return nil, errors.New("missing content-type")
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"ClientID":"en1oxy7wnw8j9n"`)) || !bytes.Contains(body, []byte(`"RefreshToken":"oldrt"`)) {
			return nil, errors.New("bad body: " + string(body))
		}
		return jsonResp(200, `{"Result":{"Token":"newat","RefreshToken":"newrt","TokenExpireAt":1786805537,"TokenExpireDuration":1209600}}`), nil
	})
	a := &auth.Auth{AccessToken: "at", RefreshToken: "oldrt", ExpiresAt: 1, ApiHost: "https://oauth.example"}
	if err := c.RefreshToken(a); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if a.AccessToken != "newat" || a.RefreshToken != "newrt" {
		t.Errorf("tokens not updated: %+v", a)
	}
	if a.ExpiresAt != 1786805537 {
		t.Errorf("expiresAt=%d", a.ExpiresAt)
	}
}

// TestRefreshTokenExchangeMilliseconds 覆盖上游 TokenExpireAt 返回毫秒的场景：
// 必须归一化为 Unix 秒后再写 auth.ExpiresAt。
func TestRefreshTokenExchangeMilliseconds(t *testing.T) {
	c := testClient(func(r *http.Request) (*http.Response, error) {
		return jsonResp(200, `{"Result":{"Token":"newat","RefreshToken":"newrt","TokenExpireAt":1786847930141,"TokenExpireDuration":1209600}}`), nil
	})
	a := &auth.Auth{AccessToken: "at", RefreshToken: "oldrt", ExpiresAt: 1, ApiHost: "https://oauth.example"}
	if err := c.RefreshToken(a); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if a.ExpiresAt != 1786847930 {
		t.Errorf("expiresAt=%d want 1786847930 (毫秒转秒)", a.ExpiresAt)
	}
}

func TestRefreshTokenIfNeededSkipsFresh(t *testing.T) {
	calls := 0
	c := testClient(func(r *http.Request) (*http.Response, error) {
		calls++
		return jsonResp(200, `{"Result":{"Token":"newat","RefreshToken":"newrt","TokenExpireAt":1786847930141}}`), nil
	})
	a := &auth.Auth{AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999, ApiHost: "https://oauth.example"}
	refreshed, err := c.RefreshTokenIfNeeded(a, 24*3600*1e9)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed {
		t.Error("fresh token should not refresh")
	}
	if calls != 0 {
		t.Errorf("ExchangeToken should not be called, calls=%d", calls)
	}
	if a.AccessToken != "at" {
		t.Error("token should remain unchanged")
	}
}

func TestRefreshTokenIfNeededRefreshesExpired(t *testing.T) {
	calls := 0
	c := testClient(func(r *http.Request) (*http.Response, error) {
		calls++
		return jsonResp(200, `{"Result":{"Token":"newat","RefreshToken":"newrt","TokenExpireAt":1786847930141}}`), nil
	})
	a := &auth.Auth{AccessToken: "at", RefreshToken: "rt", ExpiresAt: 1, ApiHost: "https://oauth.example"}
	refreshed, err := c.RefreshTokenIfNeeded(a, 24*3600*1e9)
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed || calls != 1 {
		t.Errorf("expired token should refresh once, refreshed=%v calls=%d", refreshed, calls)
	}
	if a.AccessToken != "newat" || a.RefreshToken != "newrt" {
		t.Errorf("tokens not updated: %+v", a)
	}
}

func TestRefreshTokenUsesAuthApiHost(t *testing.T) {
	var gotHost string
	c := testClient(func(r *http.Request) (*http.Response, error) {
		gotHost = r.URL.Scheme + "://" + r.URL.Host
		return jsonResp(200, `{"Result":{"Token":"newat"}}`), nil
	})
	a := &auth.Auth{AccessToken: "at", RefreshToken: "rt", ExpiresAt: 1, ApiHost: "https://custom.example"}
	if err := c.RefreshToken(a); err != nil {
		t.Fatal(err)
	}
	if gotHost != "https://custom.example" {
		t.Errorf("host=%s want auth.apiHost", gotHost)
	}
}

func TestChatStreamSendsHeadersAndRewritesBody(t *testing.T) {
	var gotAuth, gotUID, gotAppID, gotIdeVer, gotIdeCode string
	var gotBody []byte
	c := testClient(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		gotUID = r.Header.Get("X-Uid")
		gotAppID = r.Header.Get("X-App-Id")
		gotIdeVer = r.Header.Get("X-Ide-Version")
		gotIdeCode = r.Header.Get("X-Ide-Version-Code")
		gotBody, _ = io.ReadAll(r.Body)
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("event:done\ndata:{\"finish_reason\":\"stop\"}\n\n")),
		}, nil
	})
	a := &auth.Auth{AccessToken: "at", UID: "u1", MachineID: "m1", DeviceID: "d1"}
	rc, status, respBody, err := c.ChatStream(a, []byte(`{"model":"deepseek-v4.1-flash","messages":[]}`))
	if err != nil || status != 200 {
		t.Fatalf("chat: status=%d err=%v", status, err)
	}
	if respBody != nil {
		t.Errorf("200 response should carry nil body, got %q", respBody)
	}
	rc.Close()
	if gotAuth != "Cloud-IDE-JWT at" || gotUID != "u1" {
		t.Errorf("headers: auth=%q uid=%q", gotAuth, gotUID)
	}
	if gotAppID != AppID || gotIdeVer != "3.3.104" || gotIdeCode != "20260920" {
		t.Errorf("app headers: appid=%q idever=%q idecode=%q", gotAppID, gotIdeVer, gotIdeCode)
	}
	if !bytes.Contains(gotBody, []byte(`"stream":true`)) || !bytes.Contains(gotBody, []byte(`"function":"solo_work_lite"`)) {
		t.Errorf("body not rewritten: %s", gotBody)
	}
	if !bytes.Contains(gotBody, []byte(`"config_name":"deepseek-v4.1-flash"`)) {
		t.Errorf("TraeCode model not sent as config_name: %s", gotBody)
	}
}

func TestFetchModelsIncludesTraeCodeModel(t *testing.T) {
	c := testClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != EpModels {
			return nil, errors.New("wrong path: " + r.URL.Path)
		}
		if r.Header.Get("X-Ide-Version") != "3.3.104" || r.Header.Get("X-Ide-Version-Code") != "20260920" {
			return nil, errors.New("wrong TraeCode version headers")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if !bytes.Contains(body, []byte(`"function":"solo_work_lite"`)) {
			return nil, errors.New("wrong model function")
		}
		return jsonResp(200, `{"config_info_list":[{"config_name":"glm-5.2","display_config":{"display_name":"GLM-5.2"}},{"config_name":"deepseek-v4.1-flash","display_config":{"display_name":"DeepSeek-V4.1-Flash"}}]}`), nil
	})
	models, err := c.FetchModels(&auth.Auth{AccessToken: "at"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[1].ID != "deepseek-v4.1-flash" || models[1].Name != "DeepSeek-V4.1-Flash" {
		t.Errorf("models=%+v", models)
	}
}

func TestChatStreamUsesDedicatedStreamClient(t *testing.T) {
	// StreamHTTP 优先于 HTTP 被 ChatStream 使用（无总超时的长 SSE 流客户端）。
	c := testClient(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("event:done\ndata:{\"finish_reason\":\"stop\"}\n\n")),
		}, nil
	})
	c.StreamHTTP = &http.Client{Transport: c.HTTP.Transport} // 无 Timeout
	rc, status, _, err := c.ChatStream(&auth.Auth{AccessToken: "at", UID: "u1"}, []byte(`{"model":"glm-5.2","messages":[]}`))
	if err != nil || status != 200 {
		t.Fatalf("chat: status=%d err=%v", status, err)
	}
	rc.Close()
	if c.StreamHTTP.Timeout != 0 {
		t.Errorf("stream client should have no total timeout, got %v", c.StreamHTTP.Timeout)
	}
}

func TestChatStreamHTTPError(t *testing.T) {
	c := testClient(func(r *http.Request) (*http.Response, error) {
		return jsonResp(429, `rate limited`), nil
	})
	a := &auth.Auth{AccessToken: "at", UID: "u1"}
	_, status, respBody, err := c.ChatStream(a, []byte(`{}`))
	if status != 429 {
		t.Errorf("status=%d", status)
	}
	if err != nil {
		t.Fatalf("429 should come via status, err=%v", err)
	}
	if Classify(status, string(respBody)) != ErrSoftRate {
		t.Errorf("not classified soft rate: %q", respBody)
	}
}

func TestUserEntUsageAggregation(t *testing.T) {
	c := testClient(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, EpEntUsage) {
			return nil, errors.New("wrong path: " + r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Cloud-IDE-JWT at" {
			return nil, errors.New("missing auth header")
		}
		return jsonResp(200, `{"is_credits_billing":true,"user_entitlement_pack_list":[
			{"entitlement_base_info":{"quota":{"credits_limit":2000}},"usage":{"credits_amount":1500}},
			{"entitlement_base_info":{"quota":{"credits_limit":500}},"usage":{"credits_amount":500}}
		]}`), nil
	})
	remain, err := c.UserEntUsage(&auth.Auth{AccessToken: "at"})
	if err != nil {
		t.Fatalf("ent usage: %v", err)
	}
	if remain != 500 {
		t.Errorf("remain=%d want 500 (额度上限-已用)", remain)
	}
}

func TestUserEntUsageExhaustedIsZero(t *testing.T) {
	c := testClient(func(r *http.Request) (*http.Response, error) {
		return jsonResp(200, `{"is_credits_billing":true,"usage_summary":{"total_amount":8500,"consumed_amount":8500},"user_entitlement_pack_list":[
			{"display_desc":"权益包A","entitlement_base_info":{"quota":{"credits_limit":4000}},"usage":{"credits_amount":4000}},
			{"display_desc":"权益包B","entitlement_base_info":{"quota":{"credits_limit":4000}},"usage":{"credits_amount":4000}},
			{"display_desc":"权益包C","entitlement_base_info":{"quota":{"credits_limit":500}},"usage":{"credits_amount":500}}
		]}`), nil
	})
	remain, err := c.UserEntUsage(&auth.Auth{AccessToken: "at"})
	if err != nil {
		t.Fatalf("ent usage: %v", err)
	}
	if remain != 0 {
		t.Errorf("remain=%d want 0 for exhausted account", remain)
	}
}

func TestUserEntUsageWithoutUsageFieldKeepsLimit(t *testing.T) {
	c := testClient(func(r *http.Request) (*http.Response, error) {
		return jsonResp(200, `{"is_credits_billing":true,"user_entitlement_pack_list":[
			{"entitlement_base_info":{"quota":{"credits_limit":2000}}},
			{"entitlement_base_info":{"quota":{"credits_limit":500}}}
		]}`), nil
	})
	remain, err := c.UserEntUsage(&auth.Auth{AccessToken: "at"})
	if err != nil {
		t.Fatalf("ent usage: %v", err)
	}
	if remain != 2500 {
		t.Errorf("remain=%d want 2500 (无 usage 字段时退回额度上限)", remain)
	}
}

func TestUserEntUsageFallsBackToUsageSummary(t *testing.T) {
	c := testClient(func(r *http.Request) (*http.Response, error) {
		return jsonResp(200, `{"is_credits_billing":true,"usage_summary":{"total_amount":850,"consumed_amount":850}}`), nil
	})
	remain, err := c.UserEntUsage(&auth.Auth{AccessToken: "at"})
	if err != nil {
		t.Fatalf("ent usage: %v", err)
	}
	if remain != 0 {
		t.Errorf("remain=%d want 0 from usage_summary", remain)
	}
}

func TestCheckinClaimAcceptsBusinessSuccess(t *testing.T) {
	c := testClient(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, EpCheckinClaim) {
			return nil, errors.New("wrong path: " + r.URL.Path)
		}
		return jsonResp(200, `{"code":0,"message":"success"}`), nil
	})
	if err := c.CheckinClaim(&auth.Auth{AccessToken: "at"}); err != nil {
		t.Fatalf("claim should succeed: %v", err)
	}
}

func TestCheckinClaimSurfacesBusinessError(t *testing.T) {
	c := testClient(func(r *http.Request) (*http.Response, error) {
		return jsonResp(200, `{"code":9074,"message":"当前参与用户太多，请稍后再试"}`), nil
	})
	// 9074 属可重试码：这里用 1 次尝试的紧凑策略，只验证错误透传（重试行为另测）。
	err := c.CheckinClaimWithPolicy(&auth.Auth{AccessToken: "at"}, CheckinClaimRetryPolicy{MaxAttempts: 1})
	if err == nil {
		t.Fatal("HTTP 200 + code 9074 must not be treated as success")
	}
	if !strings.Contains(err.Error(), "9074") {
		t.Errorf("error should carry upstream code: %v", err)
	}
}

// TestCheckinClaimRetriesOn9074 9074 是「名额已满」类闸门，重试到底后应返回最后一次错误，
// 并且真正重试了 MaxAttempts 次（不能一次失败就放弃当天奖励）。
func TestCheckinClaimRetriesOn9074(t *testing.T) {
	var calls int
	c := testClient(func(r *http.Request) (*http.Response, error) {
		calls++
		return jsonResp(200, `{"code":9074,"message":"当前参与用户太多，请稍后再试"}`), nil
	})
	err := c.CheckinClaimWithPolicy(&auth.Auth{AccessToken: "at"}, CheckinClaimRetryPolicy{
		MaxAttempts: 4, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, Jitter: 0,
	})
	if err == nil || !strings.Contains(err.Error(), "9074") {
		t.Fatalf("want 9074 after retries, got %v", err)
	}
	if calls != 4 {
		t.Errorf("claim calls=%d want 4", calls)
	}
}

// TestCheckinClaimStopsRetryingAfterSuccess 中途抢到名额就立即停止重试。
func TestCheckinClaimStopsRetryingAfterSuccess(t *testing.T) {
	var calls int
	c := testClient(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls < 3 {
			return jsonResp(200, `{"code":9074,"message":"too many"}`), nil
		}
		return jsonResp(200, `{"code":0,"message":"success"}`), nil
	})
	if err := c.CheckinClaimWithPolicy(&auth.Auth{AccessToken: "at"}, CheckinClaimRetryPolicy{
		MaxAttempts: 8, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, Jitter: 0,
	}); err != nil {
		t.Fatalf("third attempt should win: %v", err)
	}
	if calls != 3 {
		t.Errorf("claim calls=%d want 3", calls)
	}
}

// TestCheckinClaimDoesNotRetryDeterministicRejection 确定性拒绝（非 9074 业务码）立即失败。
func TestCheckinClaimDoesNotRetryDeterministicRejection(t *testing.T) {
	var calls int
	c := testClient(func(r *http.Request) (*http.Response, error) {
		calls++
		return jsonResp(200, `{"code":40301,"message":"account banned"}`), nil
	})
	err := c.CheckinClaimWithPolicy(&auth.Auth{AccessToken: "at"}, CheckinClaimRetryPolicy{
		MaxAttempts: 5, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "40301") {
		t.Fatalf("want 40301, got %v", err)
	}
	if calls != 1 {
		t.Errorf("claim calls=%d want 1 (no retry on deterministic rejection)", calls)
	}
}

// TestCheckinClaimRetriesOn429 429 属上游抖动，应重试；会话类 401 不重试。
func TestCheckinClaimRetriesOn429(t *testing.T) {
	var calls int
	c := testClient(func(r *http.Request) (*http.Response, error) {
		calls++
		return jsonResp(429, `{"code":3004,"message":"Too Many Requests"}`), nil
	})
	_ = c.CheckinClaimWithPolicy(&auth.Auth{AccessToken: "at"}, CheckinClaimRetryPolicy{
		MaxAttempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	})
	if calls != 3 {
		t.Errorf("429 should be retried: calls=%d want 3", calls)
	}
}

func TestCheckinClaimNoRetryOn401(t *testing.T) {
	var calls int
	c := testClient(func(r *http.Request) (*http.Response, error) {
		calls++
		return jsonResp(401, `{"code":1001,"msg":"login required"}`), nil
	})
	_ = c.CheckinClaimWithPolicy(&auth.Auth{AccessToken: "at"}, CheckinClaimRetryPolicy{
		MaxAttempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	})
	if calls != 1 {
		t.Errorf("session dead must not be retried: calls=%d want 1", calls)
	}
}

// TestCheckinClaimDefaultAttemptsIsBounded 默认策略必须有限次，避免把签到入口挂死。
func TestCheckinClaimDefaultAttemptsIsBounded(t *testing.T) {
	policy := DefaultCheckinClaimRetryPolicy()
	if policy.MaxAttempts <= 1 || policy.MaxAttempts > 20 {
		t.Fatalf("default MaxAttempts=%d not within a sane bound", policy.MaxAttempts)
	}
	if policy.MaxBackoff <= 0 || policy.Jitter < 0 {
		t.Fatalf("default backoff/jitter invalid: %+v", policy)
	}
}

func TestCheckinStatusAndClaim(t *testing.T) {
	var path string
	c := testClient(func(r *http.Request) (*http.Response, error) {
		path = r.URL.Path
		if r.Header.Get("X-User-Region") != "CN" {
			return nil, errors.New("missing X-User-Region")
		}
		return jsonResp(200, `{"checked_in":false,"credits":200,"enable":true}`), nil
	})
	checkedIn, credits, enable, err := c.CheckinStatus(&auth.Auth{AccessToken: "at"})
	if err != nil {
		t.Fatal(err)
	}
	if checkedIn || !enable || credits != 200 {
		t.Errorf("status: checked=%v enable=%v credits=%d", checkedIn, enable, credits)
	}
	if path != EpCheckinStatus {
		t.Errorf("path=%s", path)
	}
}
