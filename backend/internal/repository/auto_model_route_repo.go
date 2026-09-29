package repository

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) SaveAutoModelRoute(ctx context.Context, apiKeyID int64, route service.AutoModelRouteObservation) error {
	if len(route.Candidates) > 0 {
		plan, err := json.Marshal(route.Candidates)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(plan)
		route.PlanID = fmt.Sprintf("%x", digest)
		_, err = r.sql.ExecContext(ctx, `INSERT INTO auto_model_route_plans (api_key_id, session_id, plan_id, candidates)
			VALUES ($1, $2, $3, $4) ON CONFLICT (api_key_id, session_id, plan_id) DO NOTHING`, apiKeyID, route.SessionID, route.PlanID, string(plan))
		if err != nil {
			return err
		}
	}
	attempts, err := json.Marshal(route.AttemptedModels)
	if err != nil {
		return err
	}
	_, err = r.sql.ExecContext(ctx, `INSERT INTO auto_model_routes
		(api_key_id, session_id, run_id, request_id, requested_model, selected_model, resolved_model, attempted_models, plan_id, state, started_at, updated_at, revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (api_key_id, session_id, request_id) DO UPDATE SET
		resolved_model = EXCLUDED.resolved_model, attempted_models = EXCLUDED.attempted_models,
		plan_id = EXCLUDED.plan_id, state = EXCLUDED.state, updated_at = EXCLUDED.updated_at, revision = EXCLUDED.revision
		WHERE auto_model_routes.revision < EXCLUDED.revision`,
		apiKeyID, route.SessionID, route.RunID, route.RequestID, route.RequestedModel, route.SelectedModel, route.ResolvedModel,
		string(attempts), route.PlanID, route.State, route.StartedAt, route.UpdatedAt, route.Revision)
	return err
}

func (r *usageLogRepository) ListAutoModelRoutes(ctx context.Context, apiKeyID int64, sessionID, runID string) ([]service.AutoModelRouteObservation, error) {
	args := []any{apiKeyID, sessionID}
	filter := "api_key_id = $1 AND session_id = $2"
	if runID != "" {
		args = append(args, runID)
		filter += " AND run_id = $3"
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT request_id, run_id, requested_model, selected_model, resolved_model,
		attempted_models, plan_id, state, started_at, updated_at FROM auto_model_routes WHERE `+filter+`
		ORDER BY started_at DESC, request_id DESC LIMIT 128`, args...)
	if err != nil {
		return nil, err
	}
	routes := make([]service.AutoModelRouteObservation, 0)
	for rows.Next() {
		route := service.AutoModelRouteObservation{SessionID: sessionID}
		var attempts []byte
		if err := rows.Scan(&route.RequestID, &route.RunID, &route.RequestedModel, &route.SelectedModel, &route.ResolvedModel,
			&attempts, &route.PlanID, &route.State, &route.StartedAt, &route.UpdatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		route.TurnID = route.RunID
		if err := json.Unmarshal(attempts, &route.AttemptedModels); err != nil {
			_ = rows.Close()
			return nil, err
		}
		routes = append(routes, route)
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	// 仅最新请求附带完整计划；按 run_id 查询可以取回历史回合的计划。
	if len(routes) == 0 || routes[0].PlanID == "" {
		return routes, nil
	}
	plans, err := r.sql.QueryContext(ctx, `SELECT candidates FROM auto_model_route_plans
		WHERE api_key_id = $1 AND session_id = $2 AND plan_id = $3`, apiKeyID, sessionID, routes[0].PlanID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = plans.Close() }()
	if plans.Next() {
		var plan []byte
		if err := plans.Scan(&plan); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(plan, &routes[0].Candidates); err != nil {
			return nil, err
		}
	}
	return routes, plans.Err()
}
