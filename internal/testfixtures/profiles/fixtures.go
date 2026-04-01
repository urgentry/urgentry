package profiles

import (
	"context"
	_ "embed"
	"encoding/json"
	"testing"
	"time"
)

var (
	//go:embed testdata/save_read.json
	saveReadBody []byte
	//go:embed testdata/io_heavy.json
	ioHeavyBody []byte
	//go:embed testdata/cpu_heavy.json
	cpuHeavyBody []byte
	//go:embed testdata/mixed_language.json
	mixedLanguageBody []byte
	//go:embed testdata/db_heavy.json
	dbHeavyBody []byte
	//go:embed testdata/malformed_empty.json
	malformedEmptyBody []byte
	//go:embed testdata/invalid_frames.json
	invalidFramesBody []byte
)

var defaultTimestamp = time.Date(2026, time.March, 29, 10, 0, 0, 0, time.UTC)

type Saver interface {
	SaveEnvelopeProfile(ctx context.Context, projectID string, payload []byte) (string, error)
}

type Expectations struct {
	SampleCount   int
	FrameCount    int
	FunctionCount int
	ThreadCount   int
	StackCount    int
	TopFrame      string
	TopFunction   string
}

type Fixture struct {
	Name     string
	spec     EnvelopeSpec
	Expected Expectations
}

type EnvelopeSpec struct {
	EventID     string
	ProfileID   string
	Transaction string
	TraceID     string
	Release     string
	Environment string
	Platform    string
	Timestamp   time.Time
	DurationNS  int64
	Body        json.RawMessage
}

type profileEnvelope struct {
	EventID     string          `json:"event_id,omitempty"`
	ProfileID   string          `json:"profile_id,omitempty"`
	Transaction string          `json:"transaction,omitempty"`
	TraceID     string          `json:"trace_id,omitempty"`
	Release     string          `json:"release,omitempty"`
	Environment string          `json:"environment,omitempty"`
	Platform    string          `json:"platform,omitempty"`
	Timestamp   string          `json:"timestamp,omitempty"`
	DurationNS  int64           `json:"duration_ns,omitempty"`
	Profile     json.RawMessage `json:"profile,omitempty"`
}

func Corpus() []Fixture {
	return []Fixture{CPUHeavy(), IOHeavy(), MixedLanguage()}
}

func SaveRead() Fixture {
	return Fixture{
		Name: "save_read",
		spec: defaultSpec("evt-save-read", "profile-save-read", 25000000, saveReadBody),
		Expected: Expectations{
			SampleCount:   6,
			FrameCount:    2,
			FunctionCount: 2,
			ThreadCount:   2,
			StackCount:    1,
			TopFrame:      "checkout @ checkout.go:41",
			TopFunction:   "checkout",
		},
	}
}

func IOHeavy() Fixture {
	return Fixture{
		Name: "io_heavy",
		spec: defaultSpec("evt-io-heavy", "profile-io-heavy", 12000000, ioHeavyBody),
		Expected: Expectations{
			SampleCount:   8,
			FrameCount:    4,
			FunctionCount: 4,
			ThreadCount:   1,
			StackCount:    2,
			TopFrame:      "dbQuery @ db.go:12",
			TopFunction:   "dbQuery",
		},
	}
}

func CPUHeavy() Fixture {
	return Fixture{
		Name: "cpu_heavy",
		spec: defaultSpec("evt-cpu-heavy", "profile-cpu-heavy", 15000000, cpuHeavyBody),
		Expected: Expectations{
			SampleCount:   7,
			FrameCount:    4,
			FunctionCount: 4,
			ThreadCount:   1,
			StackCount:    2,
			TopFrame:      "jsonMarshal @ encode.go:55",
			TopFunction:   "jsonMarshal",
		},
	}
}

func MixedLanguage() Fixture {
	return Fixture{
		Name: "mixed_language",
		spec: EnvelopeSpec{
			EventID:     "evt-mixed-language",
			ProfileID:   "profile-mixed-language",
			Transaction: "checkout",
			TraceID:     "0123456789abcdef0123456789abcdef",
			Release:     "backend@1.2.3",
			Environment: "production",
			Platform:    "mixed",
			Timestamp:   defaultTimestamp,
			DurationNS:  31000000,
			Body:        append(json.RawMessage(nil), mixedLanguageBody...),
		},
		Expected: Expectations{
			SampleCount:   9,
			FrameCount:    4,
			FunctionCount: 4,
			ThreadCount:   2,
			StackCount:    3,
			TopFrame:      "py:normalize_line_items @ orders/worker.py:128",
			TopFunction:   "py:normalize_line_items",
		},
	}
}

func DBHeavy() Fixture {
	return Fixture{
		Name: "db_heavy",
		spec: defaultSpec("evt-db-heavy", "profile-db-heavy", 28000000, dbHeavyBody),
		Expected: Expectations{
			SampleCount:   10,
			FrameCount:    2,
			FunctionCount: 2,
			ThreadCount:   1,
			StackCount:    2,
			TopFrame:      "dbQuery @ db.go:12",
			TopFunction:   "dbQuery",
		},
	}
}

func MalformedEmpty() Fixture {
	return Fixture{
		Name: "malformed_empty",
		spec: defaultSpec("evt-malformed-empty", "profile-malformed-empty", 1000000, malformedEmptyBody),
	}
}

func InvalidFrames() Fixture {
	return Fixture{
		Name: "invalid_frames",
		spec: defaultSpec("evt-invalid-frames", "profile-invalid-frames", 1000000, invalidFramesBody),
	}
}

func (f Fixture) Spec() EnvelopeSpec {
	return f.spec
}

func Save(tb testing.TB, saver Saver, projectID string, spec EnvelopeSpec) string {
	tb.Helper()
	profileID, err := saver.SaveEnvelopeProfile(context.Background(), projectID, spec.Payload())
	if err != nil {
		tb.Fatalf("SaveEnvelopeProfile %s: %v", spec.ProfileID, err)
	}
	return profileID
}

func (s EnvelopeSpec) Payload() []byte {
	payload := profileEnvelope{
		EventID:     s.EventID,
		ProfileID:   s.ProfileID,
		Transaction: s.Transaction,
		TraceID:     s.TraceID,
		Release:     s.Release,
		Environment: s.Environment,
		Platform:    s.Platform,
		DurationNS:  s.DurationNS,
		Profile:     append(json.RawMessage(nil), s.Body...),
	}
	if !s.Timestamp.IsZero() {
		payload.Timestamp = s.Timestamp.UTC().Format(time.RFC3339)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return body
}

func (s EnvelopeSpec) WithIDs(eventID, profileID string) EnvelopeSpec {
	s.EventID = eventID
	s.ProfileID = profileID
	return s
}

func (s EnvelopeSpec) WithTransaction(transaction string) EnvelopeSpec {
	s.Transaction = transaction
	return s
}

func (s EnvelopeSpec) WithTrace(traceID string) EnvelopeSpec {
	s.TraceID = traceID
	return s
}

func (s EnvelopeSpec) WithRelease(release string) EnvelopeSpec {
	s.Release = release
	return s
}

func (s EnvelopeSpec) WithEnvironment(environment string) EnvelopeSpec {
	s.Environment = environment
	return s
}

func (s EnvelopeSpec) WithPlatform(platform string) EnvelopeSpec {
	s.Platform = platform
	return s
}

func (s EnvelopeSpec) WithTimestamp(ts time.Time) EnvelopeSpec {
	s.Timestamp = ts
	return s
}

func (s EnvelopeSpec) WithDuration(durationNS int64) EnvelopeSpec {
	s.DurationNS = durationNS
	return s
}

func (s EnvelopeSpec) WithBody(body []byte) EnvelopeSpec {
	s.Body = append(json.RawMessage(nil), body...)
	return s
}

func defaultSpec(eventID, profileID string, durationNS int64, body []byte) EnvelopeSpec {
	return EnvelopeSpec{
		EventID:     eventID,
		ProfileID:   profileID,
		Transaction: "checkout",
		TraceID:     "0123456789abcdef0123456789abcdef",
		Release:     "backend@1.2.3",
		Environment: "production",
		Platform:    "go",
		Timestamp:   defaultTimestamp,
		DurationNS:  durationNS,
		Body:        append(json.RawMessage(nil), body...),
	}
}
