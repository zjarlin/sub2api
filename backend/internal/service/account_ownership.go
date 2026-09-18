package service

import (
	"context"
	"errors"
	"fmt"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *adminServiceImpl) validateOwnerCanBindAccountGroups(ctx context.Context, ownerUserID int64, groupIDs []int64) error {
	if ownerUserID <= 0 || len(groupIDs) == 0 {
		return nil
	}
	if s.userRepo == nil {
		return infraerrors.InternalServer("USER_REPOSITORY_UNAVAILABLE", "user repository is not configured")
	}
	if s.groupRepo == nil {
		return errors.New("group repository not configured")
	}

	user, err := s.userRepo.GetByID(ctx, ownerUserID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	activeSubscriptions := map[int64]bool{}
	for _, groupID := range groupIDs {
		group, err := s.groupRepo.GetByID(ctx, groupID)
		if err != nil {
			return fmt.Errorf("get group: %w", err)
		}
		if group.Status != StatusActive {
			return infraerrors.BadRequest("GROUP_NOT_ACTIVE", "target group is not active")
		}
		if group.IsSubscriptionType() {
			if s.userSubRepo == nil {
				return infraerrors.InternalServer("SUBSCRIPTION_REPOSITORY_UNAVAILABLE", "subscription repository is not configured")
			}
			if !activeSubscriptions[group.ID] {
				if _, err := s.userSubRepo.GetActiveByUserIDAndGroupID(ctx, ownerUserID, group.ID); err != nil {
					if errors.Is(err, ErrSubscriptionNotFound) {
						return ErrGroupNotAllowed
					}
					return err
				}
				activeSubscriptions[group.ID] = true
			}
			continue
		}
		if !user.CanBindGroup(group.ID, group.IsExclusive) {
			return ErrGroupNotAllowed
		}
	}
	return nil
}
