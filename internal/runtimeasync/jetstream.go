package runtimeasync

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	headerJobKind     = "X-Urgentry-Job-Kind"
	headerProjectID   = "X-Urgentry-Project-ID"
	defaultAckWait    = 30 * time.Second
	defaultMaxDeliver = 8
)

type JetStreamQueue struct {
	nc         *nats.Conn
	js         nats.JetStreamContext
	claimKinds []string
	subs       []*nats.Subscription
	nextSub    int
	pendingMu  sync.Mutex
	pending    map[string]*nats.Msg
}

func NewJetStreamQueue(url, consumer string, claimKinds ...string) (*JetStreamQueue, error) {
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("jetstream url is required")
	}
	if strings.TrimSpace(consumer) == "" {
		return nil, fmt.Errorf("jetstream consumer is required")
	}
	nc, err := nats.Connect(url, nats.Name("urgentry-runtime-"+consumer))
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		_ = nc.Drain()
		nc.Close()
		return nil, fmt.Errorf("jetstream context: %w", err)
	}
	q := &JetStreamQueue{
		nc:         nc,
		js:         js,
		claimKinds: normalizeKinds(claimKinds),
		pending:    map[string]*nats.Msg{},
	}
	if err := q.ensureRuntimeStreams(); err != nil {
		_ = q.Close()
		return nil, err
	}
	for _, kind := range q.claimKinds {
		stream, subject, err := jobRuntimeRoute(kind)
		if err != nil {
			_ = q.Close()
			return nil, err
		}
		sub, err := js.PullSubscribe(subject, durableName(kind),
			nats.BindStream(string(stream)),
			nats.ManualAck(),
			nats.AckWait(defaultAckWait),
			nats.MaxDeliver(defaultMaxDeliver),
		)
		if err != nil {
			_ = q.Close()
			return nil, fmt.Errorf("pull subscribe %s: %w", kind, err)
		}
		q.subs = append(q.subs, sub)
	}
	return q, nil
}

func (q *JetStreamQueue) Enqueue(ctx context.Context, kind, projectID string, payload []byte, _ int) (bool, error) {
	return q.EnqueueKeyed(ctx, kind, projectID, "", payload, 0)
}

func (q *JetStreamQueue) EnqueueKeyed(_ context.Context, kind, projectID, dedupeKey string, payload []byte, _ int) (bool, error) {
	if q == nil || q.js == nil {
		return false, fmt.Errorf("jetstream queue is not configured")
	}
	_, subject, err := jobRuntimeRoute(kind)
	if err != nil {
		return false, err
	}
	msg := &nats.Msg{
		Subject: subject,
		Data:    payload,
		Header: nats.Header{
			headerJobKind:   []string{kind},
			headerProjectID: []string{projectID},
		},
	}
	opts := []nats.PubOpt{}
	if supportsJetStreamMsgDedup(kind) && dedupeKey != "" {
		opts = append(opts, nats.MsgId(dedupeKey))
	} else {
		opts = append(opts, nats.MsgId(fmt.Sprintf("%s:%s:%d", kind, projectID, time.Now().UTC().UnixNano())))
	}
	if _, err := q.js.PublishMsg(msg, opts...); err != nil {
		return false, fmt.Errorf("publish jetstream job: %w", err)
	}
	return true, nil
}

func (q *JetStreamQueue) ClaimNext(ctx context.Context, _ string, _ time.Duration) (*Job, error) {
	if q == nil || len(q.subs) == 0 {
		return nil, nil
	}
	for i := 0; i < len(q.subs); i++ {
		sub := q.subs[(q.nextSub+i)%len(q.subs)]
		fetchCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		msgs, err := sub.Fetch(1, nats.Context(fetchCtx))
		cancel()
		if err != nil {
			if err == context.DeadlineExceeded || err == nats.ErrTimeout {
				continue
			}
			return nil, fmt.Errorf("fetch jetstream job: %w", err)
		}
		if len(msgs) == 0 {
			continue
		}
		q.nextSub = (q.nextSub + i + 1) % len(q.subs)
		msg := msgs[0]
		_ = msg.InProgress()
		meta, err := msg.Metadata()
		if err != nil {
			return nil, fmt.Errorf("jetstream metadata: %w", err)
		}
		jobID := jetstreamJobID(meta)
		q.pendingMu.Lock()
		q.pending[jobID] = msg
		q.pendingMu.Unlock()
		return &Job{
			ID:        jobID,
			Kind:      msg.Header.Get(headerJobKind),
			ProjectID: msg.Header.Get(headerProjectID),
			Payload:   append([]byte(nil), msg.Data...),
			Attempts:  int(meta.NumDelivered),
		}, nil
	}
	return nil, nil
}

func (q *JetStreamQueue) MarkDone(_ context.Context, jobID string) error {
	msg := q.takePending(jobID)
	if msg == nil {
		return nil
	}
	if err := msg.Ack(); err != nil && err != nats.ErrMsgAlreadyAckd {
		return fmt.Errorf("ack jetstream job: %w", err)
	}
	return nil
}

func (q *JetStreamQueue) Requeue(_ context.Context, jobID string, delay time.Duration, _ string) error {
	msg := q.takePending(jobID)
	if msg == nil {
		return nil
	}
	if delay <= 0 {
		delay = time.Second
	}
	if err := msg.NakWithDelay(delay); err != nil && err != nats.ErrMsgAlreadyAckd {
		return fmt.Errorf("nak jetstream job: %w", err)
	}
	return nil
}

func (q *JetStreamQueue) Len(ctx context.Context) (int, error) {
	if q == nil || q.js == nil {
		return 0, nil
	}
	total := 0
	seen := map[string]struct{}{}
	for _, kind := range q.claimKinds {
		stream, _, err := jobRuntimeRoute(kind)
		if err != nil {
			return 0, err
		}
		if _, ok := seen[string(stream)]; ok {
			continue
		}
		seen[string(stream)] = struct{}{}
		info, err := q.js.StreamInfo(string(stream), nats.Context(ctx))
		if err != nil {
			return 0, fmt.Errorf("stream info %s: %w", stream, err)
		}
		total += int(info.State.Msgs)
	}
	return total, nil
}

func (q *JetStreamQueue) RequeueExpiredProcessing(context.Context) (int64, error) {
	return 0, nil
}

func (q *JetStreamQueue) Close() error {
	if q == nil || q.nc == nil {
		return nil
	}
	err := q.nc.Drain()
	q.nc.Close()
	return err
}

func (q *JetStreamQueue) ensureRuntimeStreams() error {
	streams := []Stream{StreamProjectors, StreamOperations, StreamDeadLetter}
	for _, stream := range streams {
		subjects := make([]string, 0, len(StreamSubjects[stream]))
		for _, subject := range StreamSubjects[stream] {
			subjects = append(subjects, string(subject))
		}
		if _, err := q.js.AddStream(&nats.StreamConfig{
			Name:      string(stream),
			Subjects:  subjects,
			Retention: nats.WorkQueuePolicy,
			Storage:   nats.FileStorage,
		}); err != nil && !strings.Contains(err.Error(), "stream name already in use") {
			return fmt.Errorf("add stream %s: %w", stream, err)
		}
	}
	return nil
}

func (q *JetStreamQueue) takePending(jobID string) *nats.Msg {
	q.pendingMu.Lock()
	defer q.pendingMu.Unlock()
	msg := q.pending[jobID]
	delete(q.pending, jobID)
	return msg
}

func durableName(kind string) string {
	name := strings.NewReplacer(".", "_", "-", "_", ":", "_").Replace("urgentry_runtime_" + kind)
	if len(name) > 64 {
		return name[:64]
	}
	return name
}

func normalizeKinds(kinds []string) []string {
	if len(kinds) == 0 {
		return []string{sqliteEventKind, sqliteNativeKind}
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			continue
		}
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		result = append(result, kind)
	}
	return result
}

func jetstreamJobID(meta *nats.MsgMetadata) string {
	if meta == nil {
		return ""
	}
	return meta.Stream + ":" + strconv.FormatUint(meta.Sequence.Stream, 10)
}

func supportsJetStreamMsgDedup(kind string) bool {
	switch strings.TrimSpace(kind) {
	case sqliteNativeKind:
		return true
	default:
		return false
	}
}

const (
	sqliteEventKind  = "event"
	sqliteNativeKind = "native_stackwalk"
)

func jobRuntimeRoute(kind string) (Stream, string, error) {
	switch strings.TrimSpace(kind) {
	case sqliteEventKind:
		return StreamProjectors, string(SubjectNormalizeEvent), nil
	case sqliteNativeKind:
		return StreamOperations, string(SubjectNativeReprocess), nil
	case "backfill":
		return StreamOperations, string(SubjectMaintenanceBackfill), nil
	case "bridge_projection":
		return StreamProjectors, string(SubjectBridgeProjection), nil
	default:
		return "", "", fmt.Errorf("unsupported jetstream job kind %q", kind)
	}
}
