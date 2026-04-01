package replays

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"strings"
	"time"

	"urgentry/internal/store"
)

var (
	//go:embed testdata/core_recording.json
	coreRecordingBody []byte
	//go:embed testdata/scrubbed_recording.json
	scrubbedRecordingBody []byte
)

var defaultTimestamp = time.Date(2026, time.March, 29, 10, 0, 0, 0, time.UTC)

type Expectations struct {
	ConsoleCount    int
	NetworkCount    int
	ClickCount      int
	NavigationCount int
	ErrorCount      int
	AnchorKind      string
	Titles          []string
	PageContains    []string
	PageExcludes    []string
}

type Fixture struct {
	Name      string
	spec      EnvelopeSpec
	recording json.RawMessage
	policy    store.ReplayIngestPolicy
	Expected  Expectations
}

type EnvelopeSpec struct {
	EventID     string
	ReplayID    string
	Timestamp   time.Time
	Platform    string
	Release     string
	Environment string
	RequestURL  string
	UserEmail   string
	TraceID     string
}

type replayEnvelope struct {
	EventID     string         `json:"event_id,omitempty"`
	ReplayID    string         `json:"replay_id,omitempty"`
	Timestamp   string         `json:"timestamp,omitempty"`
	Platform    string         `json:"platform,omitempty"`
	Release     string         `json:"release,omitempty"`
	Environment string         `json:"environment,omitempty"`
	Request     map[string]any `json:"request,omitempty"`
	User        map[string]any `json:"user,omitempty"`
	Contexts    map[string]any `json:"contexts,omitempty"`
}

func Corpus() []Fixture {
	return []Fixture{
		CoreJourney(),
		ScrubbedJourney(),
	}
}

func CoreJourney() Fixture {
	return Fixture{
		Name: "core_journey",
		spec: EnvelopeSpec{
			EventID:     "11111111111111111111111111111111",
			ReplayID:    "11111111111111111111111111111111",
			Timestamp:   defaultTimestamp,
			Platform:    "javascript",
			Release:     "web@1.2.3",
			Environment: "production",
			RequestURL:  "https://shop.example.com/checkout",
			UserEmail:   "buyer@example.com",
			TraceID:     "0123456789abcdef0123456789abcdef",
		},
		recording: append(json.RawMessage(nil), coreRecordingBody...),
		Expected: Expectations{
			ConsoleCount:    1,
			NetworkCount:    1,
			ClickCount:      1,
			NavigationCount: 1,
			ErrorCount:      1,
			AnchorKind:      "error",
			Titles:          []string{"cart loaded", "POST https://api.example.com/checkout", "Pay now", "Payment failed"},
			PageContains:    []string{"Replay Player", "Payment failed", "/issues/grp-replay-linked-1/", "/traces/0123456789abcdef0123456789abcdef/"},
		},
	}
}

func ScrubbedJourney() Fixture {
	return Fixture{
		Name: "scrubbed_journey",
		spec: EnvelopeSpec{
			EventID:     "22222222222222222222222222222222",
			ReplayID:    "22222222222222222222222222222222",
			Timestamp:   defaultTimestamp.Add(5 * time.Minute),
			Platform:    "javascript",
			Release:     "web@1.2.4",
			Environment: "production",
			RequestURL:  "https://shop.example.com/account?token=secret",
			UserEmail:   "secret@example.com",
			TraceID:     "fedcba9876543210fedcba9876543210",
		},
		recording: append(json.RawMessage(nil), scrubbedRecordingBody...),
		policy: store.ReplayIngestPolicy{
			SampleRate:     1,
			MaxBytes:       4096,
			ScrubFields:    []string{"email", "token", "password"},
			ScrubSelectors: []string{".secret"},
		},
		Expected: Expectations{
			ConsoleCount:    1,
			NetworkCount:    0,
			ClickCount:      1,
			NavigationCount: 1,
			ErrorCount:      0,
			AnchorKind:      "click",
			Titles:          []string{"account loaded", "[Filtered]"},
			PageContains:    []string{"Replay Player", "[Filtered]"},
			PageExcludes:    []string{"secret@example.com", "4111111111111111", "token=secret", ".secret", "super-secret"},
		},
	}
}

func (f Fixture) Spec() EnvelopeSpec {
	return f.spec
}

func (f Fixture) Policy() store.ReplayIngestPolicy {
	return f.policy
}

func (f Fixture) RecordingPayload() []byte {
	return compactReplayFixtureJSON(f.recording)
}

func (f Fixture) EnvelopeBody() []byte {
	lines := []string{
		`{"event_id":"` + f.spec.EventID + `"}`,
		`{"type":"replay_event","length":0}`,
		string(f.spec.Payload()),
	}
	if len(f.recording) > 0 {
		recording := compactReplayFixtureJSON(f.recording)
		lines = append(lines,
			`{"type":"replay_recording","length":0,"filename":"segment-1.rrweb","content_type":"application/json"}`,
			string(recording),
		)
	}
	lines = append(lines, "")
	return []byte(strings.Join(lines, "\n"))
}

func (s EnvelopeSpec) Payload() []byte {
	payload := replayEnvelope{
		EventID:     s.EventID,
		ReplayID:    s.ReplayID,
		Platform:    s.Platform,
		Release:     s.Release,
		Environment: s.Environment,
	}
	if !s.Timestamp.IsZero() {
		payload.Timestamp = s.Timestamp.UTC().Format(time.RFC3339)
	}
	if strings.TrimSpace(s.RequestURL) != "" {
		payload.Request = map[string]any{"url": strings.TrimSpace(s.RequestURL)}
	}
	if strings.TrimSpace(s.UserEmail) != "" {
		payload.User = map[string]any{"email": strings.TrimSpace(s.UserEmail)}
	}
	if strings.TrimSpace(s.TraceID) != "" {
		payload.Contexts = map[string]any{
			"trace": map[string]any{"trace_id": strings.TrimSpace(s.TraceID)},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return body
}

func (s EnvelopeSpec) WithIDs(eventID, replayID string) EnvelopeSpec {
	s.EventID = strings.TrimSpace(eventID)
	s.ReplayID = strings.TrimSpace(replayID)
	return s
}

func (s EnvelopeSpec) WithTimestamp(ts time.Time) EnvelopeSpec {
	s.Timestamp = ts
	return s
}

func compactReplayFixtureJSON(payload []byte) []byte {
	var out bytes.Buffer
	if err := json.Compact(&out, payload); err != nil {
		return append([]byte(nil), payload...)
	}
	return out.Bytes()
}
