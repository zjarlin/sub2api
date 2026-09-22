package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 新记录以明确终态标记为准；旧恢复遥测必须有请求标识且没有同请求的最终失败。
// 空请求标识的历史记录无法可靠关联终态，保留在上游明细而不计入成功。
const opsRecoveredSuccessPredicate = `(
  e.status_code >= 200 AND e.status_code < 300
  AND (
    e.error_type = 'recovered_upstream'
    OR (
      e.error_type = 'upstream_error'
      AND e.error_message LIKE 'Recovered %'
      AND NULLIF(e.request_id, '') IS NOT NULL
      AND NOT EXISTS (
        SELECT 1 FROM ops_error_logs final
        WHERE final.request_id = e.request_id
          AND final.id <> e.id
          AND (
            COALESCE(final.status_code, 0) >= 400
            OR final.error_type = 'cyber_policy'
            OR COALESCE(final.error_message, '') NOT LIKE 'Recovered %'
          )
      )
    )
  )
)`

// 概览与列表共用条件，避免降级成功数与点击后的明细总数不一致。
func (r *opsRepository) queryRecoveredSuccessCount(ctx context.Context, filter *service.OpsDashboardFilter) (int64, error) {
	listFilter := &service.OpsErrorLogFilter{
		StartTime: &filter.StartTime, EndTime: &filter.EndTime,
		Platform: filter.Platform, GroupID: filter.GroupID,
		View: "recovered", IncludeRecoveredUpstream: true,
		ErrorPhasesAny: []string{"upstream", "account_auth", "routing"},
	}
	where, args := buildOpsErrorLogsWhere(listFilter)
	var count int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ops_error_logs e "+where, args...).Scan(&count)
	return count, err
}
