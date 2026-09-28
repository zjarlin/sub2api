package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type VibexUsage struct {
	Wallet struct {
		Balance *float64 `json:"wallet_balance"`
		Minimum *float64 `json:"min_balance_yuan"`
	} `json:"wallet"`
	Lite struct {
		Tokens     *int64   `json:"tokens"`
		TokenLimit *int64   `json:"daily_token_limit"`
		Cost       *float64 `json:"cost_yuan"`
		CostLimit  *float64 `json:"daily_cost_limit_yuan"`
	} `json:"lite"`
}

func FetchVibexUsage(ctx context.Context) (*VibexUsage, error) {
	base, key := builtinAdapterBaseURL(PlatformVibex), builtinAdapterAPIKey(PlatformVibex)
	if base == "" || key == "" {
		return nil, infraerrors.BadRequest("VIBEX_DISABLED", "Configure the built-in VibeX adapter first")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(base, "/v1")+"/internal/account/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, infraerrors.New(502, "VIBEX_UNAVAILABLE", "Unable to reach the VibeX adapter")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, infraerrors.New(502, "VIBEX_USAGE_FAILED", "Unable to read VibeX usage; check RunningHub login")
	}
	var result VibexUsage
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result); err != nil {
		return nil, infraerrors.New(502, "VIBEX_USAGE_FAILED", "Invalid VibeX usage response")
	}
	return &result, nil
}
