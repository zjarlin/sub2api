//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOwnedAccountsAlwaysParticipateInPublicScheduling(t *testing.T) {
	owner := int64(11)
	public := &Account{}
	private := &Account{OwnerUserID: &owner}
	shared := &Account{OwnerUserID: &owner, Extra: map[string]any{AccountPublicSharingExtraKey: true}}

	require.True(t, public.IsPubliclyShared())
	require.True(t, private.IsPubliclyShared())
	require.True(t, shared.IsPubliclyShared())
	require.True(t, (&Account{OwnerUserID: &owner, Extra: map[string]any{AccountPublicSharingExtraKey: false}}).IsPubliclyShared())
	require.False(t, (*Account)(nil).IsPubliclyShared())

	groupID := int64(7)
	for _, group := range []*int64{nil, &groupID} {
		private.GroupIDs = []int64{groupID}
		private.AccountGroups = []AccountGroup{{GroupID: groupID}}
		require.Equal(t, group != nil, openAIStickyAccountMatchesGroup(private, group))
		require.Equal(t, group != nil, (&GatewayService{}).isAccountInGroup(private, group))
	}
	shared.GroupIDs = []int64{groupID}
	shared.AccountGroups = []AccountGroup{{GroupID: groupID}}
	require.True(t, openAIStickyAccountMatchesGroup(shared, &groupID))
	require.True(t, (&GatewayService{}).isAccountInGroup(shared, &groupID))
	simple := &OpenAIGatewayService{cfg: &config.Config{RunMode: config.RunModeSimple}}
	require.True(t, simple.openAIAccountMatchesSchedulingGroup(private, &groupID))
	require.True(t, simple.openAIAccountMatchesSchedulingGroup(shared, &groupID))
}
