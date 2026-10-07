//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAutoModelRoutesPersistByKeySessionAndRunWithMonotonicUpdates(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: "auto-routes-" + uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-auto-" + uuid.NewString(), Name: "auto"})
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	sessionID := uuid.NewString()
	oldTime := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	plan := []service.AutoModelCandidate{{Model: "deepseek-v4.1-flash", Platform: "openai", Eligible: true, Order: 1}, {Model: "glm-5.3", Platform: "openai", Eligible: true, Order: 2}}
	route := service.AutoModelRouteObservation{
		RequestID: uuid.NewString(), SessionID: sessionID, RunID: "run-original", TurnID: "run-original",
		SelectedModel: "deepseek-v4.1-flash", ResolvedModel: "glm-5.3", AttemptedModels: []string{"deepseek-v4.1-flash", "glm-5.3"},
		State: "completed", StartedAt: oldTime, UpdatedAt: oldTime + 10, Revision: 3, Candidates: plan,
		RequestedModel: "ask", Operation: &service.VerticalOperation{Kind: "translation", Provider: "caiyun", TargetLanguage: "en"},
	}
	require.NoError(t, repo.SaveAutoModelRoute(ctx, key.ID, route))
	stale := route
	stale.State, stale.Revision, stale.ResolvedModel = "selected", 1, ""
	stale.AttemptedModels = []string{"deepseek-v4.1-flash"}
	require.NoError(t, repo.SaveAutoModelRoute(ctx, key.ID, stale))
	stale.State, stale.Revision = "responding", 2
	require.NoError(t, repo.SaveAutoModelRoute(ctx, key.ID, stale))
	for i := 0; i < 130; i++ {
		next := route
		next.RequestID, next.RunID = uuid.NewString(), "run-new"
		next.StartedAt = oldTime + int64(i) + 1
		require.NoError(t, repo.SaveAutoModelRoute(ctx, key.ID, next))
	}
	// 新仓储实例仍能读回旧回合；列表上限不是数据库保留上限。
	repo = newUsageLogRepositoryWithSQL(client, integrationDB)
	latest, err := repo.ListAutoModelRoutes(ctx, key.ID, sessionID, "")
	require.NoError(t, err)
	require.Len(t, latest, 128)
	require.Equal(t, plan, latest[0].Candidates)
	require.Empty(t, latest[1].Candidates)
	original, err := repo.ListAutoModelRoutes(ctx, key.ID, sessionID, "run-original")
	require.NoError(t, err)
	require.Len(t, original, 1)
	require.Equal(t, "completed", original[0].State)
	require.Equal(t, "glm-5.3", original[0].ResolvedModel)
	require.Equal(t, route.AttemptedModels, original[0].AttemptedModels)
	require.Equal(t, "run-original", original[0].TurnID)
	require.Equal(t, plan, original[0].Candidates)
	require.Equal(t, route.Operation, original[0].Operation)
	require.Equal(t, int64(3), original[0].Revision)
	require.Equal(t, "ask", original[0].RequestedModel)
	other, err := repo.ListAutoModelRoutes(ctx, key.ID+1, sessionID, "run-original")
	require.NoError(t, err)
	require.Empty(t, other)
	other, err = repo.ListAutoModelRoutes(ctx, key.ID, uuid.NewString(), "run-original")
	require.NoError(t, err)
	require.Empty(t, other)
	var plans, requests int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM auto_model_route_plans WHERE api_key_id = $1 AND session_id = $2", key.ID, sessionID).Scan(&plans))
	require.Equal(t, 1, plans)
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM auto_model_routes WHERE api_key_id = $1 AND session_id = $2", key.ID, sessionID).Scan(&requests))
	require.Equal(t, 131, requests)
}

func TestVerticalVideoRoutePersistsQueuedAndRunningStates(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: "vertical-video-" + uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-video-" + uuid.NewString(), Name: "video"})
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	route := service.AutoModelRouteObservation{
		RequestID: uuid.NewString(), SessionID: uuid.NewString(), RunID: "video-turn", TurnID: "video-turn",
		RequestedModel: "gpt-6-astra", SelectedModel: "grok-imagine-video", AttemptedModels: []string{"grok-imagine-video"},
		State: "queued", Revision: 1, StartedAt: 1, UpdatedAt: 1,
		Operation: &service.VerticalOperation{Kind: "video_generation", Provider: "grok", TaskID: "video-123"},
	}
	require.NoError(t, repo.SaveAutoModelRoute(ctx, key.ID, route))
	route.State, route.Revision = "running", 2
	require.NoError(t, repo.SaveAutoModelRoute(ctx, key.ID, route))
	stored, err := repo.ListAutoModelRoutes(ctx, key.ID, route.SessionID, route.RunID)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, "running", stored[0].State)
	require.Equal(t, route.Operation, stored[0].Operation)
}
