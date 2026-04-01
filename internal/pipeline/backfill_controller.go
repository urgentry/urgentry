package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"urgentry/internal/runtimeasync"
	"urgentry/internal/sqlite"
	"urgentry/internal/telemetrybridge"

	"github.com/rs/zerolog/log"
)

const (
	backfillLeaseDuration = 30 * time.Second
	backfillChunkSize     = 1
	backfillIdlePoll      = 100 * time.Millisecond
)

type BackfillController struct {
	runs      *sqlite.BackfillStore
	debugFile *sqlite.DebugFileStore
	telemetry telemetryRebuilder
	queue     runtimeasync.Queue
	enqueuer  runtimeasync.KeyedEnqueuer
	workerID  string
	chunkSize int
}

type telemetryRebuilder interface {
	ResetScope(ctx context.Context, scope telemetrybridge.Scope, families ...telemetrybridge.Family) error
	StepFamilies(ctx context.Context, scope telemetrybridge.Scope, families ...telemetrybridge.Family) (telemetrybridge.StepResult, error)
	EstimateFamilies(ctx context.Context, scope telemetrybridge.Scope, families ...telemetrybridge.Family) (int, error)
}

func NewBackfillController(runs *sqlite.BackfillStore, debugFile *sqlite.DebugFileStore, workerID string) *BackfillController {
	return &BackfillController{
		runs:      runs,
		debugFile: debugFile,
		workerID:  workerID,
		chunkSize: backfillChunkSize,
	}
}

func (c *BackfillController) SetQueue(queue runtimeasync.Queue) {
	c.queue = queue
}

func (c *BackfillController) SetEnqueuer(enqueuer runtimeasync.KeyedEnqueuer) {
	c.enqueuer = enqueuer
}

func (c *BackfillController) SetTelemetryRebuilder(rebuilder telemetryRebuilder) {
	c.telemetry = rebuilder
}

func (c *BackfillController) Run(ctx context.Context) {
	if c == nil || c.runs == nil {
		<-ctx.Done()
		return
	}
	if c.queue != nil {
		c.runQueued(ctx)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		advanced, err := c.RunOnce(ctx)
		if err != nil {
			log.Error().Err(err).Str("worker_id", c.workerID).Msg("backfill: step failed")
			time.Sleep(backfillIdlePoll)
			continue
		}
		if !advanced {
			time.Sleep(backfillIdlePoll)
		}
	}
}

func (c *BackfillController) runQueued(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		job, err := c.queue.ClaimNext(ctx, c.workerID, backfillLeaseDuration)
		if err != nil {
			log.Error().Err(err).Str("worker_id", c.workerID).Msg("backfill: claim job failed")
			time.Sleep(backfillIdlePoll)
			continue
		}
		if job == nil {
			time.Sleep(backfillIdlePoll)
			continue
		}
		if job.Kind != sqlite.JobKindBackfill {
			if err := c.queue.MarkDone(ctx, job.ID); err != nil {
				log.Error().Err(err).Str("worker_id", c.workerID).Str("job_id", job.ID).Str("kind", job.Kind).Msg("backfill: mark done failed for non-backfill job")
			}
			continue
		}
		advanced, err := c.RunOnce(ctx)
		if err != nil {
			if requeueErr := c.queue.Requeue(ctx, job.ID, retryDelay(max(1, job.Attempts)), err.Error()); requeueErr != nil {
				log.Error().Err(requeueErr).Str("worker_id", c.workerID).Msg("backfill: requeue job failed")
			}
			continue
		}
		if err := c.queue.MarkDone(ctx, job.ID); err != nil {
			log.Error().Err(err).Str("worker_id", c.workerID).Msg("backfill: mark job done failed")
		}
		if advanced {
			if err := c.enqueueTick(ctx); err != nil {
				log.Error().Err(err).Str("worker_id", c.workerID).Msg("backfill: enqueue tick failed")
			}
		}
	}
}

func (c *BackfillController) enqueueTick(ctx context.Context) error {
	if c == nil || c.enqueuer == nil {
		return nil
	}
	_, err := c.enqueuer.EnqueueKeyed(ctx, sqlite.JobKindBackfill, "", "backfill:tick", []byte("{}"), 1)
	return err
}

func (c *BackfillController) RunOnce(ctx context.Context) (bool, error) {
	if c == nil || c.runs == nil {
		return false, nil
	}
	run, err := c.runs.ClaimNextRunnable(ctx, c.workerID, backfillLeaseDuration)
	if err != nil || run == nil {
		return false, err
	}
	if err := c.processRun(ctx, run); err != nil {
		if markErr := c.runs.MarkFailed(ctx, run.ID, c.workerID, err.Error()); markErr != nil && !errors.Is(markErr, sqlite.ErrBackfillLeaseLost) {
			log.Error().Err(markErr).Str("worker_id", c.workerID).Str("run_id", run.ID).Msg("backfill: mark failed")
		}
		return true, err
	}
	return true, nil
}

func (c *BackfillController) processRun(ctx context.Context, run *sqlite.BackfillRun) error {
	switch run.Kind {
	case sqlite.BackfillKindNativeReprocess:
		if c.debugFile == nil {
			return fmt.Errorf("native debug file store unavailable")
		}
		totalItems := run.TotalItems
		filter := sqlite.NativeReprocessFilter{
			OrganizationID: run.OrganizationID,
			ProjectID:      run.ProjectID,
			ReleaseVersion: run.ReleaseVersion,
			StartedAfter:   run.StartedAfter,
			EndedBefore:    run.EndedBefore,
		}
		if run.TotalItems == 0 && run.ProcessedItems == 0 && run.UpdatedItems == 0 && run.FailedItems == 0 {
			total, err := c.debugFile.CountNativeReprocessCandidates(ctx, filter)
			if err != nil {
				return err
			}
			if _, err := c.runs.SetTotalItems(ctx, run.ID, c.workerID, total); err != nil {
				if errors.Is(err, sqlite.ErrBackfillLeaseLost) {
					return nil
				}
				return err
			}
			totalItems = total
		}
		result, err := c.debugFile.ReprocessNativeEventBatch(ctx, sqlite.NativeReprocessBatch{
			Filter:      filter,
			AfterRowID:  run.CursorRowID,
			Limit:       c.chunkSize,
			RunID:       run.ID,
			UserID:      run.RequestedByUserID,
			DebugFileID: run.DebugFileID,
		})
		if err != nil {
			return err
		}
		done := result.Done
		if !done && totalItems > 0 && run.ProcessedItems+result.Processed >= totalItems {
			done = true
		}
		if _, err := c.runs.AdvanceRun(ctx, run.ID, c.workerID, result.NextRowID, result.Processed, result.Updated, result.Failed, done, result.LastError); err != nil {
			if errors.Is(err, sqlite.ErrBackfillLeaseLost) {
				return nil
			}
			return err
		}
		return nil
	case sqlite.BackfillKindTelemetryRebuild:
		if c.telemetry == nil {
			return fmt.Errorf("telemetry rebuild unavailable")
		}
		scope := telemetrybridge.Scope{
			OrganizationID: run.OrganizationID,
			ProjectID:      run.ProjectID,
		}
		if run.TotalItems == 0 && run.ProcessedItems == 0 && run.UpdatedItems == 0 && run.FailedItems == 0 {
			if err := c.telemetry.ResetScope(ctx, scope); err != nil {
				return err
			}
			total, err := c.telemetry.EstimateFamilies(ctx, scope)
			if err != nil {
				return err
			}
			if _, err := c.runs.SetTotalItems(ctx, run.ID, c.workerID, total); err != nil {
				if errors.Is(err, sqlite.ErrBackfillLeaseLost) {
					return nil
				}
				return err
			}
		}
		result, err := c.telemetry.StepFamilies(ctx, scope)
		if err != nil {
			return err
		}
		if _, err := c.runs.AdvanceRun(ctx, run.ID, c.workerID, run.CursorRowID+int64(result.Processed), result.Processed, result.Processed, 0, result.Done, ""); err != nil {
			if errors.Is(err, sqlite.ErrBackfillLeaseLost) {
				return nil
			}
			return err
		}
		return nil
	default:
		return fmt.Errorf("unsupported backfill kind %q", run.Kind)
	}
}
