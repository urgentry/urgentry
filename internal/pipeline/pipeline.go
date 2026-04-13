// Package pipeline provides async event processing backed by either an
// in-memory channel (tests) or a durable SQLite queue (runtime).
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"urgentry/internal/issue"
	"urgentry/internal/metrics"
	"urgentry/internal/normalize"
	"urgentry/internal/runtimeasync"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"

	"github.com/rs/zerolog/log"
)

/*
Pipeline lifecycle

	enqueue -> queue / durable job
	        -> worker goroutines
	        -> issue processor
	        -> optional native job processor
	        -> optional alert callback

	Start freezes config before workers run.
	Stop cancels workers, drains alerts, and closes lifecycle state.
*/

// Item is a unit of work submitted to the pipeline.
type Item struct {
	ProjectID  string
	RawEvent   []byte
	EnqueuedAt time.Time
}

// Result holds the outcome of processing one pipeline item.
type Result struct {
	EventID      string
	GroupID      string
	IsNewGroup   bool
	IsRegression bool
	Err          error
}

// AlertCallback is invoked after successful event processing. Implementations
// should be non-blocking.
type AlertCallback func(ctx context.Context, projectID string, result issue.ProcessResult)

// ProjectionCallback is invoked after a source-of-truth write succeeds.
// It enqueues durable bridge projection work for the affected families.
type ProjectionCallback func(ctx context.Context, projectID string, eventType string)

type NativeJobProcessor interface {
	ProcessNativeJob(ctx context.Context, projectID string, payload []byte) error
}

type NativeJobProcessorFunc func(ctx context.Context, projectID string, payload []byte) error

func (f NativeJobProcessorFunc) ProcessNativeJob(ctx context.Context, projectID string, payload []byte) error {
	return f(ctx, projectID, payload)
}

const (
	defaultLeaseDuration = 30 * time.Second
	defaultIdlePoll      = 100 * time.Millisecond
	defaultMaxEnqueue    = time.Second
	defaultEnqueueRetry  = 25 * time.Millisecond
	alertDispatchTimeout = 250 * time.Millisecond
)

const alertQueueSize = 256

// Pipeline is an async event processing pipeline.
type Pipeline struct {
	lifecycleMu        sync.Mutex
	state              lifecycleState
	processor          *issue.Processor
	jobQueue           runtimeasync.Queue
	queueSize          int
	queue              chan Item
	workers            int
	workerWG           sync.WaitGroup
	alertWG            sync.WaitGroup
	cancel             context.CancelFunc
	alertCallback      AlertCallback
	projectionCallback ProjectionCallback
	alertQueue         chan alertInvocation
	stopCh             chan struct{}
	alertStopCh        chan struct{}
	metrics            *metrics.Metrics
	workerID           string
	nativeJobs         NativeJobProcessor
	filterStore        FilterStore
	outcomeStore       *sqlite.OutcomeStore

	// Queue timing parameters (configurable for benchmarks).
	idlePollInterval     time.Duration
	maxEnqueueWait       time.Duration
	enqueueRetryInterval time.Duration
}

type lifecycleState uint8

const (
	stateReady lifecycleState = iota
	stateStarted
	stateStopped
)

type alertInvocation struct {
	ctx       context.Context
	projectID string
	result    issue.ProcessResult
}

// New creates an in-memory pipeline with the given processor and configuration.
func New(processor *issue.Processor, queueSize, numWorkers int) *Pipeline {
	return newPipeline(processor, nil, queueSize, numWorkers)
}

// NewDurable creates a SQLite-backed durable pipeline.
func NewDurable(processor *issue.Processor, jobs runtimeasync.Queue, queueSize, numWorkers int) *Pipeline {
	return newPipeline(processor, jobs, queueSize, numWorkers)
}

func newPipeline(processor *issue.Processor, jobs runtimeasync.Queue, queueSize, numWorkers int) *Pipeline {
	if queueSize <= 0 {
		queueSize = 1000
	}
	if numWorkers <= 0 {
		numWorkers = 1
	}
	p := &Pipeline{
		processor:            processor,
		jobQueue:             jobs,
		queueSize:            queueSize,
		workers:              numWorkers,
		workerID:             fmt.Sprintf("worker-%s", sqliteID()),
		idlePollInterval:     defaultIdlePoll,
		maxEnqueueWait:       defaultMaxEnqueue,
		enqueueRetryInterval: defaultEnqueueRetry,
		stopCh:               make(chan struct{}),
		alertStopCh:          make(chan struct{}),
	}
	if jobs == nil {
		p.queue = make(chan Item, queueSize)
	}
	return p
}

// SetMetrics attaches a Metrics instance for pipeline instrumentation.
// Must be called before Start.
func (p *Pipeline) SetMetrics(m *metrics.Metrics) {
	p.mustBeConfigurable("SetMetrics")
	p.metrics = m
	if p.processor != nil {
		p.processor.Metrics = m
	}
}

// SetAlertCallback registers a function to be called after event processing.
// Must be called before Start.
func (p *Pipeline) SetAlertCallback(cb AlertCallback) {
	p.mustBeConfigurable("SetAlertCallback")
	p.alertCallback = cb
}

func (p *Pipeline) SetNativeJobProcessor(proc NativeJobProcessor) {
	p.mustBeConfigurable("SetNativeJobProcessor")
	p.nativeJobs = proc
}

// SetFilterStore attaches a filter store for inbound data filtering.
// Must be called before Start.
func (p *Pipeline) SetFilterStore(fs FilterStore) {
	p.mustBeConfigurable("SetFilterStore")
	p.filterStore = fs
}

// SetOutcomeStore attaches an outcome store for recording filtered events.
// Must be called before Start.
func (p *Pipeline) SetOutcomeStore(os *sqlite.OutcomeStore) {
	p.mustBeConfigurable("SetOutcomeStore")
	p.outcomeStore = os
}

// SetProjectionCallback registers a function to enqueue bridge projection
// work after each successful source-of-truth write. Must be called before Start.
func (p *Pipeline) SetProjectionCallback(cb ProjectionCallback) {
	p.mustBeConfigurable("SetProjectionCallback")
	p.projectionCallback = cb
}

// Start launches worker goroutines. The pipeline processes items until
// Stop is called or the context is cancelled.
func (p *Pipeline) Start(ctx context.Context) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if p.state == stateStarted {
		return
	}
	if p.state == stateStopped {
		panic("pipeline: Start called after Stop")
	}
	ctx, p.cancel = context.WithCancel(ctx)
	if p.alertCallback != nil && p.alertQueue == nil {
		p.alertQueue = make(chan alertInvocation, alertQueueSize)
		p.alertWG.Add(1)
		go p.alertWorker()
	}
	p.state = stateStarted
	for i := 0; i < p.workers; i++ {
		p.workerWG.Add(1)
		if p.jobQueue != nil {
			go p.durableWorker(ctx, fmt.Sprintf("%s-%d", p.workerID, i+1))
			continue
		}
		go p.worker(ctx)
	}
}

// Stop closes the input channel and waits for all in-flight items to finish.
// Workers drain remaining items before exiting. Safe to call multiple times.
func (p *Pipeline) Stop() {
	p.lifecycleMu.Lock()
	if p.state == stateStopped {
		p.lifecycleMu.Unlock()
		return
	}
	p.state = stateStopped
	stopCh := p.stopCh
	cancel := p.cancel
	alertQueue := p.alertQueue
	alertStopCh := p.alertStopCh
	p.lifecycleMu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}
	if cancel != nil {
		cancel()
	}
	p.workerWG.Wait()
	if alertQueue != nil {
		close(alertStopCh)
		p.alertWG.Wait()
	}
}

func (p *Pipeline) mustBeConfigurable(action string) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if p.state != stateReady {
		panic("pipeline: " + action + " must be called before Start")
	}
}

// Enqueue submits an item for async processing.
// Returns false if the pipeline is stopped (channel closed).
func (p *Pipeline) Enqueue(item Item) bool {
	return p.EnqueueContext(context.Background(), item)
}

// EnqueueContext submits an item for async processing and honors ctx while
// waiting for queue capacity.
func (p *Pipeline) EnqueueContext(ctx context.Context, item Item) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if item.EnqueuedAt.IsZero() {
		item.EnqueuedAt = time.Now()
	}
	if p.isStopped() {
		if p.metrics != nil {
			p.metrics.RecordDrop()
		}
		return false
	}
	if p.jobQueue != nil {
		enqueueStarted := time.Now()
		var enqueueErr error
		defer func() {
			if p.metrics != nil {
				p.metrics.RecordStage(metrics.StageEnqueue, time.Since(enqueueStarted), enqueueErr)
			}
		}()
		deadline := time.Now().Add(p.maxEnqueueWait)
		for {
			ok, err := p.jobQueue.Enqueue(ctx, sqlite.JobKindEvent, item.ProjectID, item.RawEvent, p.queueSize)
			if err != nil {
				enqueueErr = err
				log.Error().Err(err).Str("project_id", item.ProjectID).Msg("pipeline: durable enqueue failed")
				return false
			}
			if ok {
				if p.metrics != nil {
					p.metrics.RecordQueued()
				}
				return true
			}
			if time.Now().After(deadline) {
				if p.metrics != nil {
					p.metrics.RecordDrop()
				}
				enqueueErr = context.DeadlineExceeded
				log.Warn().Str("project_id", item.ProjectID).Dur("max_wait", p.maxEnqueueWait).Msg("pipeline: durable enqueue timed out")
				return false
			}
			select {
			case <-ctx.Done():
				if p.metrics != nil {
					p.metrics.RecordDrop()
				}
				enqueueErr = ctx.Err()
				return false
			case <-time.After(p.enqueueRetryInterval):
			}
		}
	}

	queue, ok := p.inMemoryQueue()
	if !ok {
		if p.metrics != nil {
			p.metrics.RecordDrop()
		}
		return false
	}
	select {
	case queue <- item:
		if p.metrics != nil {
			p.metrics.RecordStage(metrics.StageEnqueue, time.Since(item.EnqueuedAt), nil)
			p.metrics.RecordQueued()
		}
		return true
	default:
		if p.metrics != nil {
			p.metrics.RecordStage(metrics.StageEnqueue, time.Since(item.EnqueuedAt), context.DeadlineExceeded)
			p.metrics.RecordDrop()
		}
		return false
	}
}

// EnqueueNonBlocking tries to enqueue without blocking.
// Returns false if the queue is full or closed.
func (p *Pipeline) EnqueueNonBlocking(item Item) bool {
	if p.isStopped() {
		if p.metrics != nil {
			p.metrics.RecordDrop()
		}
		return false
	}
	if p.jobQueue != nil {
		ok, err := p.jobQueue.Enqueue(context.Background(), sqlite.JobKindEvent, item.ProjectID, item.RawEvent, p.queueSize)
		if err != nil {
			log.Error().Err(err).Str("project_id", item.ProjectID).Msg("pipeline: durable enqueue failed")
			if p.metrics != nil {
				p.metrics.RecordDrop()
			}
			return false
		}
		if ok {
			if p.metrics != nil {
				p.metrics.RecordQueued()
			}
			return true
		}
		if p.metrics != nil {
			p.metrics.RecordDrop()
		}
		return false
	}

	queue, ok := p.inMemoryQueue()
	if !ok {
		if p.metrics != nil {
			p.metrics.RecordDrop()
		}
		return false
	}
	select {
	case queue <- item:
		if p.metrics != nil {
			p.metrics.RecordQueued()
		}
		return true
	default:
		if p.metrics != nil {
			p.metrics.RecordDrop()
		}
		return false
	}
}

// Len returns the current number of items waiting in the queue.
func (p *Pipeline) Len() int {
	if p.jobQueue != nil {
		n, err := p.jobQueue.Len(context.Background())
		if err != nil {
			return 0
		}
		return n
	}
	return len(p.queue)
}

func (p *Pipeline) worker(ctx context.Context) {
	defer p.workerWG.Done()
	for {
		select {
		case <-p.stopCh:
			p.drainQueue(ctx)
			return
		default:
		}
		select {
		case item := <-p.queue:
			if p.metrics != nil && !item.EnqueuedAt.IsZero() {
				p.metrics.RecordStage(metrics.StageQueueWait, time.Since(item.EnqueuedAt), nil)
			}
			if err := p.processItem(ctx, item); err != nil {
				log.Error().Err(err).Str("project_id", item.ProjectID).Msg("pipeline: failed to process item")
			}
		case <-p.stopCh:
			p.drainQueue(ctx)
			return
		case <-ctx.Done():
			return
		}
	}
}

func (p *Pipeline) durableWorker(ctx context.Context, workerID string) {
	defer p.workerWG.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := p.jobQueue.ClaimNext(ctx, workerID, defaultLeaseDuration)
		if err != nil {
			log.Error().Err(err).Str("worker_id", workerID).Msg("pipeline: claim job failed")
			time.Sleep(p.idlePollInterval)
			continue
		}
		if job == nil {
			time.Sleep(p.idlePollInterval)
			continue
		}

		var processErr error
		switch job.Kind {
		case sqlite.JobKindEvent:
			if p.metrics != nil && !job.CreatedAt.IsZero() {
				p.metrics.RecordStage(metrics.StageQueueWait, time.Since(job.CreatedAt), nil)
			}
			processErr = p.processItem(ctx, Item{ProjectID: job.ProjectID, RawEvent: job.Payload, EnqueuedAt: job.CreatedAt})
		case sqlite.JobKindNativeStackwalk:
			if p.nativeJobs == nil {
				processErr = fmt.Errorf("native job processor is not configured")
				break
			}
			processErr = p.nativeJobs.ProcessNativeJob(ctx, job.ProjectID, job.Payload)
		case sqlite.JobKindBridgeProjection:
			// Bridge projection jobs are handled by the dedicated projection
			// worker when running in JetStream mode. If we receive one here
			// it means the queue was shared -- silently skip.
			if err := p.jobQueue.MarkDone(ctx, job.ID); err != nil {
				log.Error().Err(err).Str("job_id", job.ID).Msg("pipeline: mark done failed for bridge projection job")
			}
			continue
		default:
			log.Warn().Str("job_id", job.ID).Str("kind", job.Kind).Msg("pipeline: unsupported job kind")
			if err := p.jobQueue.MarkDone(ctx, job.ID); err != nil {
				log.Error().Err(err).Str("job_id", job.ID).Str("kind", job.Kind).Msg("pipeline: mark done failed for unsupported job kind")
			}
			continue
		}

		if processErr != nil {
			if isPermanentJobError(processErr) {
				if markErr := p.jobQueue.MarkDone(ctx, job.ID); markErr != nil {
					log.Error().Err(markErr).Str("job_id", job.ID).Msg("pipeline: mark done failed")
				}
				continue
			}
			backoff := retryDelay(job.Attempts)
			if requeueErr := p.jobQueue.Requeue(ctx, job.ID, backoff, processErr.Error()); requeueErr != nil {
				log.Error().Err(requeueErr).Str("job_id", job.ID).Msg("pipeline: requeue failed")
			}
			continue
		}

		if err := p.jobQueue.MarkDone(ctx, job.ID); err != nil {
			log.Error().Err(err).Str("job_id", job.ID).Msg("pipeline: mark done failed")
		}
	}
}

func (p *Pipeline) processItem(ctx context.Context, item Item) error {
	if alreadyProcessed, err := p.isDuplicateCompleted(ctx, item); err != nil {
		return err
	} else if alreadyProcessed {
		return nil
	}

	// Check inbound data filters before processing.
	if p.filterStore != nil {
		evt, err := normalize.Normalize(item.RawEvent)
		if err == nil {
			if reason := checkFilters(ctx, p.filterStore, item.ProjectID, evt); reason != nil {
				log.Debug().
					Str("project_id", item.ProjectID).
					Str("filter", reason.FilterID).
					Msg("pipeline: event filtered")
				recordFilteredOutcome(ctx, p.outcomeStore, item.ProjectID, evt.EventID, reason)
				if p.metrics != nil {
					p.metrics.RecordDrop()
				}
				return nil
			}
		}
	}

	start := time.Now()
	result, err := p.processor.Process(ctx, item.ProjectID, item.RawEvent)
	duration := time.Since(start)

	if err != nil {
		if p.metrics != nil {
			p.metrics.RecordProcessingFailure(duration, err)
		}
		log.Error().Err(err).Str("project_id", item.ProjectID).Msg("pipeline: process event failed")
		return err
	}

	if p.metrics != nil {
		p.metrics.RecordProcessing(duration, result.IsNewGroup, result.IsRegression)
	}

	// Run performance issue detection for transaction events after processing.
	if result.EventType == "transaction" {
		if evt, err := normalize.Normalize(item.RawEvent); err == nil {
			detectPerformanceIssues(evt)
		}
	}

	if p.projectionCallback != nil {
		p.projectionCallback(ctx, item.ProjectID, result.EventType)
	}
	if p.alertCallback != nil {
		p.dispatchAlert(alertInvocation{ctx: ctx, projectID: item.ProjectID, result: *result})
	}
	return nil
}

func (p *Pipeline) isDuplicateCompleted(ctx context.Context, item Item) (bool, error) {
	if p == nil || p.processor == nil || p.processor.Events == nil {
		return false, nil
	}
	evt, err := normalize.Normalize(item.RawEvent)
	if err != nil {
		return false, nil
	}
	switch evt.EventType() {
	case "transaction":
		type traceGetter interface {
			GetTransaction(ctx context.Context, projectID, eventID string) (*store.StoredTransaction, error)
		}
		getter, ok := any(p.processor.Traces).(traceGetter)
		if !ok {
			return false, nil
		}
		item, err := getter.GetTransaction(ctx, item.ProjectID, evt.EventID)
		if err != nil {
			return false, nil
		}
		return item != nil, nil
	default:
		existing, err := p.processor.Events.GetEvent(ctx, item.ProjectID, evt.EventID)
		if err != nil {
			return false, nil
		}
		return existing != nil && existing.ProcessingStatus == store.EventProcessingStatusCompleted, nil
	}
}

func (p *Pipeline) dispatchAlert(inv alertInvocation) {
	if p.alertQueue == nil {
		p.alertCallback(inv.ctx, inv.projectID, inv.result)
		return
	}
	timer := time.NewTimer(alertDispatchTimeout)
	defer timer.Stop()
	select {
	case p.alertQueue <- inv:
		if p.metrics != nil {
			p.metrics.RecordAlertDispatchQueued()
		}
	case <-p.alertStopCh:
		if p.metrics != nil {
			p.metrics.RecordAlertDispatchDropped()
		}
	case <-timer.C:
		if p.metrics != nil {
			p.metrics.RecordAlertDispatchDropped()
		}
		log.Warn().Str("project_id", inv.projectID).Dur("timeout", alertDispatchTimeout).Msg("pipeline: dropping alert callback under backpressure")
	case <-inv.ctx.Done():
	}
}

func (p *Pipeline) alertWorker() {
	defer p.alertWG.Done()
	for {
		select {
		case inv := <-p.alertQueue:
			p.alertCallback(inv.ctx, inv.projectID, inv.result)
		case <-p.alertStopCh:
			for {
				select {
				case inv := <-p.alertQueue:
					p.alertCallback(inv.ctx, inv.projectID, inv.result)
				default:
					return
				}
			}
		}
	}
}

func (p *Pipeline) isStopped() bool {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	return p.state == stateStopped
}

func (p *Pipeline) inMemoryQueue() (chan Item, bool) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if p.state == stateStopped || p.queue == nil {
		return nil, false
	}
	return p.queue, true
}

func (p *Pipeline) drainQueue(ctx context.Context) {
	for {
		select {
		case item := <-p.queue:
			if err := p.processItem(ctx, item); err != nil {
				log.Error().Err(err).Str("project_id", item.ProjectID).Msg("pipeline: failed to process item during drain")
			}
		default:
			return
		}
	}
}

func retryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := time.Second << minInt(attempts-1, 5)
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func sqliteID() string {
	return time.Now().UTC().Format("20060102150405")
}

func isPermanentJobError(err error) bool {
	type permanent interface {
		Permanent() bool
	}
	var marker permanent
	if !errors.As(err, &marker) {
		return false
	}
	return marker.Permanent()
}
