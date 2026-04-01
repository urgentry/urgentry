package telemetrybridge

import (
	"context"
	"fmt"
)

func projectorOutcomesFamily() projectorFamily {
	return projectorFamily{
		sync: (*Projector).syncOutcomes,
		count: func(p *Projector, ctx context.Context, scope Scope) (int, error) {
			return p.countSourceRows(ctx, FamilyOutcomes, `SELECT COUNT(*) FROM outcomes o JOIN projects p ON p.id = o.project_id WHERE p.organization_id = ? AND (? = '' OR o.project_id = ?)`, scope.OrganizationID, scope.ProjectID, scope.ProjectID)
		},
		clear: func(p *Projector, ctx context.Context, scope Scope) error {
			return p.deleteBridgeRows(ctx, FamilyOutcomes, `DELETE FROM telemetry.outcome_facts WHERE `, scope)
		},
	}
}

func (p *Projector) syncOutcomes(ctx context.Context, scope Scope, checkpoint int64) (StepResult, error) {
	rows, err := p.source.QueryContext(ctx, `
		SELECT o.rowid, o.id, p.organization_id, o.project_id, COALESCE(o.reason, ''), o.category, o.quantity,
		       COALESCE(o.recorded_at, ''), COALESCE(o.source, 'client_report')
		  FROM outcomes o
		  JOIN projects p ON p.id = o.project_id
		 WHERE p.organization_id = ?
		   AND (? = '' OR o.project_id = ?)
		   AND o.rowid > ?
		 ORDER BY o.rowid ASC
		 LIMIT ?`,
		scope.OrganizationID,
		scope.ProjectID, scope.ProjectID,
		checkpoint,
		p.batchSize,
	)
	if err != nil {
		return StepResult{}, fmt.Errorf("query outcome projector batch: %w", err)
	}
	defer rows.Close()

	return p.withBridgeBatch(ctx, scope, FamilyOutcomes, func(exec bridgeExecer) (int, int64, error) {
		processed := 0
		last := checkpoint
		for rows.Next() {
			var rowID int64
			var id, organizationID, projectID, reason, category, recordedAt, source string
			var quantity int64
			if err := rows.Scan(&rowID, &id, &organizationID, &projectID, &reason, &category, &quantity, &recordedAt, &source); err != nil {
				return 0, 0, fmt.Errorf("scan outcome projector batch: %w", err)
			}
			outcome := "filtered"
			if _, err := exec.ExecContext(ctx, `
			INSERT INTO telemetry.outcome_facts
				(id, organization_id, project_id, outcome, reason, category, quantity, recorded_at, source)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9)
			ON CONFLICT (id) DO UPDATE SET
				outcome = EXCLUDED.outcome,
				reason = EXCLUDED.reason,
				category = EXCLUDED.category,
				quantity = EXCLUDED.quantity,
				recorded_at = EXCLUDED.recorded_at,
				source = EXCLUDED.source`,
				id, organizationID, projectID, outcome, reason, category, quantity, mustTimestamp(recordedAt), source,
			); err != nil {
				return 0, 0, fmt.Errorf("upsert telemetry outcome fact: %w", err)
			}
			last = rowID
			processed++
		}
		if err := rows.Err(); err != nil {
			return 0, 0, fmt.Errorf("iterate outcome projector batch: %w", err)
		}
		return processed, last, nil
	})
}
