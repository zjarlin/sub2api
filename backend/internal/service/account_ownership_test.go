//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

type ownershipUserRepo struct {
	UserRepository
	user *User
}

func (s ownershipUserRepo) GetByID(context.Context, int64) (*User, error) { return s.user, nil }

type ownershipGroupRepo struct {
	GroupRepository
	group *Group
}

func (s ownershipGroupRepo) GetByID(context.Context, int64) (*Group, error) { return s.group, nil }

type ownershipSubscriptionRepo struct{ UserSubscriptionRepository }

func (s ownershipSubscriptionRepo) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	return nil, ErrSubscriptionNotFound
}

func TestAccountOwnershipGroupPermissions(t *testing.T) {
	group := &Group{ID: 7, Status: StatusActive, IsExclusive: true}
	svc := &adminServiceImpl{userRepo: ownershipUserRepo{user: &User{ID: 11}}, groupRepo: ownershipGroupRepo{group: group}, userSubRepo: ownershipSubscriptionRepo{}}
	require.ErrorIs(t, svc.validateOwnerCanBindAccountGroups(context.Background(), 11, []int64{7}), ErrGroupNotAllowed)
	group.IsExclusive = false
	require.NoError(t, svc.validateOwnerCanBindAccountGroups(context.Background(), 11, []int64{7}))
	group.Status = "inactive"
	require.Error(t, svc.validateOwnerCanBindAccountGroups(context.Background(), 11, []int64{7}))
	group.Status = StatusActive
	group.SubscriptionType = "subscription"
	require.ErrorIs(t, svc.validateOwnerCanBindAccountGroups(context.Background(), 11, []int64{7}), ErrGroupNotAllowed)
}
