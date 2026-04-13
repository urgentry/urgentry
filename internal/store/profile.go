package store

import (
	"context"
	"encoding/json"
	"time"
)

type ProfileProcessingStatus string

const (
	ProfileProcessingStatusCompleted ProfileProcessingStatus = "completed"
	ProfileProcessingStatusFailed    ProfileProcessingStatus = "failed"
)

// ProfileIngestStore accepts raw profile envelope payloads and materializes
// them into the canonical profile storage model.
type ProfileIngestStore interface {
	SaveEnvelopeProfile(ctx context.Context, projectID string, payload []byte) (string, error)
}

// ProfileReadStore exposes canonical profile reads and query projections.
type ProfileReadStore interface {
	ListProfiles(ctx context.Context, projectID string, limit int) ([]ProfileManifest, error)
	GetProfile(ctx context.Context, projectID, profileID string) (*ProfileRecord, error)
	FindProfilesByTrace(ctx context.Context, projectID, traceID string, limit int) ([]ProfileReference, error)
	ListReleaseProfileHighlights(ctx context.Context, projectID, release string, limit int) ([]ProfileReference, error)
	FindRelatedProfile(ctx context.Context, projectID, traceID, transaction, release string) (*ProfileReference, error)
	QueryTopDown(ctx context.Context, projectID string, filter ProfileQueryFilter) (*ProfileTree, error)
	QueryBottomUp(ctx context.Context, projectID string, filter ProfileQueryFilter) (*ProfileTree, error)
	QueryFlamegraph(ctx context.Context, projectID string, filter ProfileQueryFilter) (*ProfileTree, error)
	QueryHotPath(ctx context.Context, projectID string, filter ProfileQueryFilter) (*ProfileHotPath, error)
	CompareProfiles(ctx context.Context, projectID string, filter ProfileComparisonFilter) (*ProfileComparison, error)
}

// ProfileManifest is the canonical metadata row for one stored profile.
type ProfileManifest struct {
	ID               string
	EventRowID       string
	ProjectID        string
	EventID          string
	ProfileID        string
	TraceID          string
	Transaction      string
	Release          string
	Environment      string
	Platform         string
	ProfileKind      string
	StartedAt        time.Time
	EndedAt          time.Time
	DurationNS       int64
	ThreadCount      int
	SampleCount      int
	FrameCount       int
	FunctionCount    int
	StackCount       int
	ProcessingStatus ProfileProcessingStatus
	IngestError      string
	RawBlobKey       string
	DateCreated      time.Time
}

// ProfileThread is one normalized logical execution thread.
type ProfileThread struct {
	ID          string
	ManifestID  string
	ThreadKey   string
	ThreadName  string
	ThreadRole  string
	IsMain      bool
	SampleCount int
	DurationNS  int64
}

// ProfileFrame is one normalized code location within a single profile.
type ProfileFrame struct {
	ID            string
	ManifestID    string
	FrameKey      string
	FrameLabel    string
	FunctionLabel string
	FunctionName  string
	ModuleName    string
	PackageName   string
	Filename      string
	Lineno        int
	InApp         bool
	ImageRef      string
}

// ProfileStack is one normalized ordered frame sequence.
type ProfileStack struct {
	ID          string
	ManifestID  string
	StackKey    string
	LeafFrameID string
	RootFrameID string
	Depth       int
}

// ProfileStackFrame links a stack to one frame at a stable ordinal position.
type ProfileStackFrame struct {
	ManifestID string
	StackID    string
	Position   int
	FrameID    string
}

// ProfileSample is one normalized sample row.
type ProfileSample struct {
	ID          string
	ManifestID  string
	ThreadRowID string
	StackID     string
	TSNS        int64
	Weight      int
	WallTimeNS  int64
	QueueTimeNS int64
	CPUTimeNS   int64
	IsIdle      bool
}

// ProfileBreakdown is a ranked aggregate entry, such as a top frame/function.
type ProfileBreakdown struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ProfileSummary captures the useful breakdown from one profile payload.
type ProfileSummary struct {
	Transaction   string             `json:"transaction,omitempty"`
	TraceID       string             `json:"traceId,omitempty"`
	Platform      string             `json:"platform,omitempty"`
	Release       string             `json:"release,omitempty"`
	Environment   string             `json:"environment,omitempty"`
	DurationNS    string             `json:"durationNs,omitempty"`
	SampleCount   int                `json:"sampleCount"`
	FrameCount    int                `json:"frameCount"`
	FunctionCount int                `json:"functionCount"`
	TopFrames     []ProfileBreakdown `json:"topFrames,omitempty"`
	TopFunctions  []ProfileBreakdown `json:"topFunctions,omitempty"`
}

// ProfileRecord is the query-ready detail shape used by API and web layers.
type ProfileRecord struct {
	Manifest     ProfileManifest
	RawPayload   json.RawMessage
	Threads      []ProfileThread
	Frames       []ProfileFrame
	Stacks       []ProfileStack
	StackFrames  []ProfileStackFrame
	Samples      []ProfileSample
	TopFrames    []ProfileBreakdown
	TopFunctions []ProfileBreakdown
}

// ProfileReference is the lightweight profile summary used by trace, release,
// and alert-linking surfaces.
type ProfileReference struct {
	ProjectID      string
	EventID        string
	ProfileID      string
	TraceID        string
	Transaction    string
	Release        string
	Environment    string
	Platform       string
	DurationNS     int64
	SampleCount    int
	FunctionCount  int
	StartedAt      time.Time
	TopFunction    string
	TopFunctionCnt int
}

type ProfileQueryFilter struct {
	ProfileID    string
	ThreadID     string
	FrameFilter  string
	Transaction  string
	Release      string
	Environment  string
	StartedAfter time.Time
	EndedBefore  time.Time
	MaxDepth     int
	MaxNodes     int
}

type ProfileTree struct {
	ProfileID    string          `json:"profile_id"`
	ThreadID     string          `json:"thread_id"`
	Mode         string          `json:"mode"`
	TotalWeight  int             `json:"total_weight"`
	TotalSamples int             `json:"total_samples"`
	Truncated    bool            `json:"truncated"`
	Root         ProfileTreeNode `json:"root"`
}

type ProfileTreeNode struct {
	Name            string            `json:"name"`
	FrameID         string            `json:"frame_id"`
	InclusiveWeight int               `json:"inclusive_weight"`
	SelfWeight      int               `json:"self_weight"`
	SampleCount     int               `json:"sample_count"`
	Children        []ProfileTreeNode `json:"children"`
}

type ProfileHotPath struct {
	ProfileID    string                `json:"profile_id"`
	ThreadID     string                `json:"thread_id"`
	TotalWeight  int                   `json:"total_weight"`
	TotalSamples int                   `json:"total_samples"`
	Truncated    bool                  `json:"truncated"`
	Frames       []ProfileHotPathFrame `json:"frames"`
}

type ProfileHotPathFrame struct {
	Name            string  `json:"name"`
	FrameID         string  `json:"frame_id"`
	InclusiveWeight int     `json:"inclusive_weight"`
	SampleCount     int     `json:"sample_count"`
	Percent         float64 `json:"percent"`
}

type ProfileComparisonFilter struct {
	BaselineProfileID  string
	CandidateProfileID string
	ThreadID           string
	MaxFunctions       int
}

type ProfileComparison struct {
	BaselineProfileID    string                   `json:"baseline_profile_id"`
	CandidateProfileID   string                   `json:"candidate_profile_id"`
	ThreadID             string                   `json:"thread_id"`
	DurationDeltaNS      int64                    `json:"duration_delta_ns"`
	SampleCountDelta     int                      `json:"sample_count_delta"`
	Confidence           string                   `json:"confidence"`
	Notes                []string                 `json:"notes"`
	TopRegressions       []ProfileComparisonDelta `json:"top_regressions"`
	TopImprovements      []ProfileComparisonDelta `json:"top_improvements"`
	SharedFunctionLabels int                      `json:"shared_function_labels"`
	TotalFunctionLabels  int                      `json:"total_function_labels"`
}

type ProfileComparisonDelta struct {
	Name            string `json:"name"`
	BaselineWeight  int    `json:"baseline_weight"`
	CandidateWeight int    `json:"candidate_weight"`
	DeltaWeight     int    `json:"delta_weight"`
}
