package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecoveryNeverSelectsClosedSchedulingSwitch(t *testing.T) {
	for _, status := range []string{StatusActive, StatusError, StatusDisabled} {
		t.Run(status, func(t *testing.T) {
			until := time.Now().Add(time.Hour)
			account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: status, Schedulable: false, Concurrency: 1,
				TempUnschedulableUntil: &until, Extra: map[string]any{"openai_passthrough": true}}
			svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}}
			selection, _, err := svc.selectOpenAIRecoveryAccount(context.Background(), OpenAIAccountScheduleRequest{
				Platform: PlatformOpenAI, RequestedModel: "gpt-6-astra",
			})
			require.NoError(t, err)
			require.Nil(t, selection)
		})
	}
}
