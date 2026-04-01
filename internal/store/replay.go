package store

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ReplayProcessingStatus string

const (
	ReplayProcessingStatusReady   ReplayProcessingStatus = "ready"
	ReplayProcessingStatusPartial ReplayProcessingStatus = "partial"
	ReplayProcessingStatusFailed  ReplayProcessingStatus = "failed"
)

// ReplayIngestStore accepts replay envelope payloads and materializes
// canonical replay manifests, asset references, and timeline indexes.
type ReplayIngestStore interface {
	SaveEnvelopeReplay(ctx context.Context, projectID, fallbackEventID string, payload []byte) (string, error)
	IndexReplay(ctx context.Context, projectID, replayID string) error
}

// ReplayReadStore exposes canonical replay reads for playback-oriented APIs.
type ReplayReadStore interface {
	ListReplays(ctx context.Context, projectID string, limit int) ([]ReplayManifest, error)
	GetReplay(ctx context.Context, projectID, replayID string) (*ReplayRecord, error)
	ListReplayTimeline(ctx context.Context, projectID, replayID string, filter ReplayTimelineFilter) ([]ReplayTimelineItem, error)
}

type ReplayPolicyStore interface {
	GetReplayIngestPolicy(ctx context.Context, projectID string) (ReplayIngestPolicy, error)
}

type ReplayIngestPolicy struct {
	SampleRate     float64  `json:"sampleRate"`
	MaxBytes       int64    `json:"maxBytes"`
	ScrubFields    []string `json:"scrubFields,omitempty"`
	ScrubSelectors []string `json:"scrubSelectors,omitempty"`
}

func CanonicalReplayIngestPolicy(input ReplayIngestPolicy) (ReplayIngestPolicy, error) {
	policy := input
	if policy.SampleRate == 0 && policy.MaxBytes == 0 && len(policy.ScrubFields) == 0 && len(policy.ScrubSelectors) == 0 {
		policy.SampleRate = 1.0
	}
	if policy.SampleRate < 0 || policy.SampleRate > 1 {
		return ReplayIngestPolicy{}, ErrInvalidReplayPolicy("sampleRate must be between 0 and 1")
	}
	if policy.MaxBytes <= 0 {
		policy.MaxBytes = 10 << 20
	}
	policy.ScrubFields = canonicalReplayStrings(policy.ScrubFields)
	policy.ScrubSelectors = canonicalReplayStrings(policy.ScrubSelectors)
	return policy, nil
}

type replayPolicyError string

func (e replayPolicyError) Error() string { return string(e) }

func ErrInvalidReplayPolicy(message string) error {
	return replayPolicyError(message)
}

func IsInvalidReplayPolicy(err error) bool {
	var target replayPolicyError
	return errors.As(err, &target)
}

func (p ReplayIngestPolicy) PolicyVersion() string {
	canonical, err := CanonicalReplayIngestPolicy(p)
	if err != nil {
		return ""
	}
	sum := sha1.Sum([]byte(
		strconv.FormatFloat(canonical.SampleRate, 'f', 6, 64) + "|" +
			strconv.FormatInt(canonical.MaxBytes, 10) + "|" +
			strings.Join(canonical.ScrubFields, ",") + "|" +
			strings.Join(canonical.ScrubSelectors, ","),
	))
	return hex.EncodeToString(sum[:8])
}

func canonicalReplayStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	items := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		items = append(items, value)
	}
	sort.Strings(items)
	return items
}

type ReplayUserRef struct {
	ID       string `json:"id,omitempty"`
	Email    string `json:"email,omitempty"`
	Username string `json:"username,omitempty"`
}

type ReplayAssetRef struct {
	ID           string    `json:"id"`
	ReplayID     string    `json:"replay_id"`
	AttachmentID string    `json:"attachment_id"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	ContentType  string    `json:"content_type,omitempty"`
	SizeBytes    int64     `json:"size_bytes"`
	ObjectKey    string    `json:"object_key,omitempty"`
	ChunkIndex   int       `json:"chunk_index"`
	CreatedAt    time.Time `json:"created_at"`
}

type ReplayManifest struct {
	ID                   string
	EventRowID           string
	ProjectID            string
	ReplayID             string
	Platform             string
	Release              string
	Environment          string
	StartedAt            time.Time
	EndedAt              time.Time
	DurationMS           int64
	RequestURL           string
	UserRef              ReplayUserRef
	TraceIDs             []string
	LinkedEventIDs       []string
	LinkedIssueIDs       []string
	AssetCount           int
	ConsoleCount         int
	NetworkCount         int
	ClickCount           int
	NavigationCount      int
	ErrorMarkerCount     int
	TimelineStartMS      int64
	TimelineEndMS        int64
	PrivacyPolicyVersion string
	ProcessingStatus     ReplayProcessingStatus
	IngestError          string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ReplayTimelineItem struct {
	ID            string          `json:"id"`
	ReplayID      string          `json:"replay_id"`
	TSMS          int64           `json:"ts_ms"`
	ItemIndex     int             `json:"item_index"`
	Kind          string          `json:"kind"`
	Pane          string          `json:"pane"`
	Title         string          `json:"title,omitempty"`
	Level         string          `json:"level,omitempty"`
	Message       string          `json:"message,omitempty"`
	URL           string          `json:"url,omitempty"`
	Method        string          `json:"method,omitempty"`
	StatusCode    int             `json:"status_code,omitempty"`
	DurationMS    int64           `json:"duration_ms,omitempty"`
	Selector      string          `json:"selector,omitempty"`
	Text          string          `json:"text,omitempty"`
	TraceID       string          `json:"trace_id,omitempty"`
	LinkedEventID string          `json:"linked_event_id,omitempty"`
	LinkedIssueID string          `json:"linked_issue_id,omitempty"`
	PayloadRef    string          `json:"payload_ref,omitempty"`
	MetaJSON      json.RawMessage `json:"meta_json,omitempty"`
}

type ReplayRecord struct {
	Manifest ReplayManifest       `json:"manifest"`
	Assets   []ReplayAssetRef     `json:"assets"`
	Timeline []ReplayTimelineItem `json:"timeline"`
	Payload  json.RawMessage      `json:"payload,omitempty"`
}

type ReplayTimelineFilter struct {
	Pane    string
	Kind    string
	StartMS int64
	EndMS   int64
	Limit   int
	EventID string
	TraceID string
	IssueID string
	Search  string
}
