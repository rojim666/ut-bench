package store

import (
	"context"
	"fmt"
	"strings"
)

// DeleteRun 删除一个 run 在 SQLite 中的索引数据。
// 磁盘文件由调用方负责删除；这里仅清理会影响列表、复用和报告聚合的记录。
func (s *SQLiteStore) DeleteRun(ctx context.Context, runID string) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("run_id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := nowUTC()
	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `DELETE FROM report_result_members
				WHERE report_id IN (SELECT report_id FROM report_snapshots WHERE run_id = ?)`,
			args: []any{runID},
		},
		{
			query: `DELETE FROM report_snapshots WHERE run_id = ?`,
			args:  []any{runID},
		},
		{
			query: `DELETE FROM evaluation_stage_results
				WHERE evaluation_result_id IN (SELECT evaluation_result_id FROM evaluation_results WHERE run_id = ?)`,
			args: []any{runID},
		},
		{
			query: `DELETE FROM evaluation_results WHERE run_id = ?`,
			args:  []any{runID},
		},
		{
			query: `DELETE FROM evaluation_runs WHERE run_id = ?`,
			args:  []any{runID},
		},
		{
			query: `DELETE FROM prompt_renderings WHERE run_id = ?`,
			args:  []any{runID},
		},
		{
			query: `DELETE FROM generated_cases WHERE run_id = ?`,
			args:  []any{runID},
		},
		{
			query: `DELETE FROM generation_runs WHERE run_id = ?`,
			args:  []any{runID},
		},
		{
			query: `UPDATE artifacts
				SET deleted_at_utc = ?
				WHERE artifact_id IN (SELECT artifact_id FROM run_artifacts WHERE run_id = ?)
				  AND artifact_id NOT IN (SELECT artifact_id FROM run_artifacts WHERE run_id <> ?)`,
			args: []any{now, runID, runID},
		},
		{
			query: `DELETE FROM run_artifacts WHERE run_id = ?`,
			args:  []any{runID},
		},
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			return fmt.Errorf("delete run %s: %w", runID, err)
		}
	}
	return tx.Commit()
}
