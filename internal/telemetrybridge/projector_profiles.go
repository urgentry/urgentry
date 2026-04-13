package telemetrybridge

import (
	"context"
	"fmt"
)

func projectorProfilesFamily() projectorFamily {
	return projectorFamily{
		sync: (*Projector).syncProfiles,
		count: func(p *Projector, ctx context.Context, scope Scope) (int, error) {
			return p.countSourceRows(ctx, FamilyProfiles, `SELECT COUNT(*) FROM profile_manifests pm JOIN projects p ON p.id = pm.project_id WHERE p.organization_id = ? AND (? = '' OR pm.project_id = ?)`, scope.OrganizationID, scope.ProjectID, scope.ProjectID)
		},
		clear: func(p *Projector, ctx context.Context, scope Scope) error {
			return p.deleteBridgeRows(ctx, FamilyProfiles, `DELETE FROM telemetry.profile_manifests WHERE `, scope)
		},
	}
}

func (p *Projector) syncProfiles(ctx context.Context, scope Scope, checkpoint int64) (StepResult, error) {
	rows, err := p.source.QueryContext(ctx, `
		SELECT pm.rowid, pm.id, p.organization_id, pm.project_id, pm.profile_id, COALESCE(pm.event_id, ''),
		       COALESCE(pm.trace_id, ''), COALESCE(pm.transaction_name, ''), COALESCE(pm.release, ''),
		       COALESCE(pm.environment, ''), COALESCE(pm.platform, ''), COALESCE(pm.profile_kind, ''),
		       COALESCE(pm.started_at, pm.created_at, ''), COALESCE(pm.ended_at, ''),
		       pm.duration_ns, pm.sample_count, pm.thread_count, pm.frame_count, pm.function_count, pm.stack_count,
		       COALESCE(pm.processing_status, 'completed'), COALESCE(pm.ingest_error, ''), COALESCE(pm.raw_blob_key, ''),
		       COALESCE(pm.created_at, ''), COALESCE(NULLIF(e.payload_json, ''), '{}')
		  FROM profile_manifests pm
		  JOIN projects p ON p.id = pm.project_id
		  LEFT JOIN events e ON e.id = pm.event_row_id
		 WHERE p.organization_id = ?
		   AND (? = '' OR pm.project_id = ?)
		   AND pm.rowid > ?
		 ORDER BY pm.rowid ASC
		 LIMIT ?`,
		scope.OrganizationID,
		scope.ProjectID, scope.ProjectID,
		checkpoint,
		p.batchSize,
	)
	if err != nil {
		return StepResult{}, fmt.Errorf("query profile projector batch: %w", err)
	}
	defer rows.Close()

	return p.withBridgeBatch(ctx, scope, FamilyProfiles, func(exec bridgeExecer) (int, int64, error) {
		processed := 0
		last := checkpoint
		for rows.Next() {
			var rowID int64
			var (
				id, organizationID, projectID, profileID, eventID, traceID, transactionName string
				release, environment, platform, profileKind, startedAt, endedAt             string
				processingStatus, ingestError, rawBlobKey, createdAt, payloadJSON           string
			)
			var durationNS int64
			var sampleCount, threadCount, frameCount, functionCount, stackCount int
			if err := rows.Scan(&rowID, &id, &organizationID, &projectID, &profileID, &eventID, &traceID, &transactionName, &release, &environment, &platform, &profileKind, &startedAt, &endedAt, &durationNS, &sampleCount, &threadCount, &frameCount, &functionCount, &stackCount, &processingStatus, &ingestError, &rawBlobKey, &createdAt, &payloadJSON); err != nil {
				return 0, 0, fmt.Errorf("scan profile projector batch: %w", err)
			}
			if _, err := exec.ExecContext(ctx, `
			INSERT INTO telemetry.profile_manifests
				(id, organization_id, project_id, profile_id, event_id, trace_id, transaction_name, release, environment, platform, profile_kind, started_at, ended_at, duration_ns, sample_count, thread_count, frame_count, function_count, stack_count, processing_status, ingest_error, raw_blob_key, created_at, payload_json)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),NULLIF($11,''),$12,$13,$14,$15,$16,$17,$18,$19,$20,NULLIF($21,''),NULLIF($22,''),$23,$24::jsonb)
			ON CONFLICT (id) DO UPDATE SET
				profile_id = EXCLUDED.profile_id,
				event_id = EXCLUDED.event_id,
				trace_id = EXCLUDED.trace_id,
				transaction_name = EXCLUDED.transaction_name,
				release = EXCLUDED.release,
				environment = EXCLUDED.environment,
				platform = EXCLUDED.platform,
				profile_kind = EXCLUDED.profile_kind,
				started_at = EXCLUDED.started_at,
				ended_at = EXCLUDED.ended_at,
				duration_ns = EXCLUDED.duration_ns,
				sample_count = EXCLUDED.sample_count,
				thread_count = EXCLUDED.thread_count,
				frame_count = EXCLUDED.frame_count,
				function_count = EXCLUDED.function_count,
				stack_count = EXCLUDED.stack_count,
				processing_status = EXCLUDED.processing_status,
				ingest_error = EXCLUDED.ingest_error,
				raw_blob_key = EXCLUDED.raw_blob_key,
				created_at = EXCLUDED.created_at,
				payload_json = EXCLUDED.payload_json`,
				id, organizationID, projectID, profileID, eventID, traceID, transactionName, release, environment, platform, profileKind, mustTimestamp(startedAt), nullTimestampArg(endedAt), durationNS, sampleCount, threadCount, frameCount, functionCount, stackCount, processingStatus, ingestError, rawBlobKey, nullTimestampArg(createdAt), payloadJSON,
			); err != nil {
				return 0, 0, fmt.Errorf("upsert telemetry profile manifest: %w", err)
			}
			last = rowID
			processed++
		}
		if err := rows.Err(); err != nil {
			return 0, 0, fmt.Errorf("iterate profile projector batch: %w", err)
		}
		return processed, last, nil
	})
}
