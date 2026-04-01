package issue

import (
	"context"
	"encoding/json"
	"testing"

	"urgentry/internal/nativesym"
	"urgentry/internal/store"
)

func TestProcessor_NativeSymbolication(t *testing.T) {
	ctx := context.Background()

	resolver := nativesym.NewResolver(&nativeStubStore{
		debug: &nativesym.File{ID: "dbg-1", Kind: "macho"},
		body: []byte(`MODULE mac arm64 IOS-DEBUG-1 AppBinary
FILE 0 Sources/AppDelegate.swift
FUNC 1000 20 0 applicationMain
1000 20 87 0
`),
	})

	proc := &Processor{
		Events: store.NewMemoryEventStore(),
		Groups: NewMemoryGroupStore(),
		Blobs:  store.NewMemoryBlobStore(),
		Native: resolver,
	}

	payload := map[string]any{
		"platform": "native",
		"level":    "fatal",
		"release":  "ios@1.2.3",
		"exception": map[string]any{
			"values": []map[string]any{
				{
					"type":  "EXC_BAD_ACCESS",
					"value": "segv",
					"stacktrace": map[string]any{
						"frames": []map[string]any{
							{
								"filename":         "0x00001000",
								"module":           "AppBinary",
								"package":          "IOS-DEBUG-1",
								"instruction_addr": "0x1005",
								"debug_id":         "IOS-DEBUG-1",
								"in_app":           true,
							},
						},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(payload)

	result, err := proc.Process(ctx, "proj-ios", raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	evt, err := proc.Events.GetEvent(ctx, "proj-ios", result.EventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}

	var normalized map[string]any
	if err := json.Unmarshal(evt.NormalizedJSON, &normalized); err != nil {
		t.Fatalf("unmarshal normalized: %v", err)
	}

	exception := normalized["exception"].(map[string]any)
	values := exception["values"].([]any)
	frame := values[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)[0].(map[string]any)

	if frame["module"] != "AppBinary" {
		t.Fatalf("module = %v, want AppBinary", frame["module"])
	}
	if frame["filename"] != "Sources/AppDelegate.swift" {
		t.Fatalf("filename = %v, want Sources/AppDelegate.swift", frame["filename"])
	}
	if frame["function"] != "applicationMain" {
		t.Fatalf("function = %v, want applicationMain", frame["function"])
	}
	if int(frame["lineno"].(float64)) != 87 {
		t.Fatalf("lineno = %v, want 87", frame["lineno"])
	}
	if frame["abs_path"] != "IOS-DEBUG-1" {
		t.Fatalf("abs_path = %v, want IOS-DEBUG-1", frame["abs_path"])
	}
}

type nativeStubStore struct {
	debug *nativesym.File
	body  []byte
}

func (s *nativeStubStore) LookupByDebugID(_ context.Context, _, _, _, debugID string) (*nativesym.File, []byte, error) {
	if s.debug != nil && debugID == "IOS-DEBUG-1" {
		return s.debug, s.body, nil
	}
	return nil, nil, nil
}

func (s *nativeStubStore) LookupByCodeID(_ context.Context, _, _, _, codeID string) (*nativesym.File, []byte, error) {
	if s.debug != nil && s.debug.CodeID == codeID {
		return s.debug, s.body, nil
	}
	return nil, nil, nil
}
