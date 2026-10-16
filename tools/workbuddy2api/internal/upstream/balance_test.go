package upstream

import (
	"context"
	"net/http"
	"testing"

	"workbuddy2api/internal/auth"
)

func TestUserResourceBalancePreservesPrecision(t *testing.T) {
	for _, tc := range []struct {
		name, packages string
		want           float64
	}{
		{"周期精确余额", `{"CycleCapacitySize":100,"CycleCapacityRemain":79,"CycleCapacityRemainPrecise":"79.75","CapacityRemainPrecise":"100"}`, 79.75},
		{"精确零值优先", `{"CycleCapacitySize":100,"CycleCapacityRemain":79,"CycleCapacityRemainPrecise":"0"}`, 0},
		{"非周期套餐", `{"CapacityRemain":12,"CapacityRemainPrecise":"12.34"}`, 12.34},
		{"兼容整数余额", `{"CycleCapacitySize":100,"CycleCapacityRemain":79}`, 79},
		{"多套餐聚合", `{"CapacityRemainPrecise":"0.1"},{"CapacityRemainPrecise":"0.2"}`, 0.3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(func(r *http.Request) (*http.Response, error) {
				return mkDetailedResp(tc.packages), nil
			})
			got, err := c.UserResourceBalance(context.Background(), &auth.Auth{AccessToken: "at"})
			if err != nil || got != tc.want {
				t.Fatalf("balance=%v err=%v want=%v", got, err, tc.want)
			}
		})
	}
}

func TestUserResourceBalancePropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := testClient(func(r *http.Request) (*http.Response, error) {
		if r.Context().Err() == nil {
			t.Fatal("查询未传递请求取消")
		}
		return nil, r.Context().Err()
	})
	if _, err := c.UserResourceBalance(ctx, &auth.Auth{AccessToken: "at"}); err == nil {
		t.Fatal("查询取消必须返回错误")
	}
}
