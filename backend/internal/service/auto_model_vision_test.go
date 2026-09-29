package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoModelVisionAdmissionAndScheduling(t *testing.T) {
	for _, input := range []string{
		`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/image.png"}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}]}`,
		`{"input":[{"type":"function_call_output","call_id":"c1","output":[{"type":"input_image","image_url":"https://example.com/image.png"}]}]}`,
	} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/assisted=%t", input, enabled), func(t *testing.T) {
				primary := visionTestAccount(1, "text-model", "text")
				helper := visionTestAccount(2, "vision-model", "text", "image")
				cfg := visionTestConfig()
				cfg.Gateway.VisionFallback.Enabled = enabled
				group := &Group{ID: 7, Platform: PlatformOpenAI}
				ctx := context.WithValue(context.Background(), autoModelAccountsKey{}, &autoModelInventory{
					groupID: group.ID, accounts: []Account{primary, helper},
				})
				svc := &GatewayService{cfg: cfg, accountRepo: codexModelsVisibilityAccountRepo{}}
				ctx, err := svc.BindAutoModelVisionCapabilities(WithAutoModelRequestCapabilities(ctx, []byte(input)), group)
				require.NoError(t, err)
				compatible, err := svc.AutoModelAccountCompatible(ctx, &group.ID, PlatformOpenAI, "text-model", []byte(input))
				require.NoError(t, err)
				require.Equal(t, enabled, compatible)
				compatible, err = svc.AutoModelAccountCompatible(ctx, &group.ID, PlatformOpenAI, "vision-model", []byte(input))
				require.NoError(t, err)
				require.True(t, compatible)
				for _, account := range []*Account{&primary, &helper} {
					want := enabled || account.ID == helper.ID
					reason := openAICompatibleAccountEligibilityFailureReasonBeforeProfit(ctx, account, PlatformOpenAI, account.Name, false, "")
					require.Equal(t, want, reason == "", reason)
					scheduler := &defaultOpenAIAccountScheduler{}
					got, reason := scheduler.isAccountRequestCompatibleReason(ctx, account, OpenAIAccountScheduleRequest{RequestedModel: account.Name})
					require.Equal(t, want, got, reason)
				}
			})
		}
	}
}

func TestAutoModelVisionDoesNotCountBase64AsTextContext(t *testing.T) {
	account := visionTestAccount(1, "vision-model", "text", "image")
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"vision-model": {ID: "vision-model", InputModalities: []string{"text", "image"}, ContextWindow: 2048},
	}})
	for _, input := range []string{
		`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,%s"}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}`,
		`{"input":[{"type":"function_call_output","call_id":"c1","output":[{"type":"input_image","image_url":"data:image/png;base64,%s"}]}]}`,
	} {
		body := []byte(fmt.Sprintf(input, strings.Repeat("AAAA", 4096)))
		require.True(t, ModelAccountCompatible(&account, account.Name, body))
	}
	body := []byte(fmt.Sprintf(`{"input":%q}`, strings.Repeat("text", 1024)))
	require.False(t, ModelAccountCompatible(&account, account.Name, body))
}

func TestAutoModelVisionRequiresUsableGroupHelper(t *testing.T) {
	primary := visionTestAccount(1, "text-model", "text")
	for _, tc := range []struct {
		name      string
		configure func(*Account, *Group)
	}{
		{name: "unavailable", configure: func(a *Account, _ *Group) { a.Schedulable = false }},
		{name: "excluded by group", configure: func(_ *Account, g *Group) {
			g.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"auto", "text-model"}}
		}},
		{name: "no native vision", configure: func(a *Account, _ *Group) { a.Extra = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			helper := visionTestAccount(2, "vision-model", "text", "image")
			group := &Group{ID: 7, Platform: PlatformOpenAI}
			tc.configure(&helper, group)
			ctx := context.WithValue(context.Background(), autoModelAccountsKey{}, &autoModelInventory{groupID: group.ID, accounts: []Account{primary, helper}})
			svc := &GatewayService{cfg: visionTestConfig(), accountRepo: codexModelsVisibilityAccountRepo{}}
			ctx, err := svc.BindAutoModelVisionCapabilities(WithAutoModelRequestCapabilities(ctx, []byte(visionTestInput)), group)
			require.NoError(t, err)
			compatible, err := svc.AutoModelAccountCompatible(ctx, &group.ID, PlatformOpenAI, primary.Name, []byte(visionTestInput))
			require.NoError(t, err)
			require.False(t, compatible)
		})
	}
}
