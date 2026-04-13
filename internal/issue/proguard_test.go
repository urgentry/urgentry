package issue

import (
	"context"
	"encoding/json"
	"testing"

	"urgentry/internal/normalize"
	"urgentry/internal/proguard"
	"urgentry/internal/store"
)

func TestProcessor_ProGuardResolution(t *testing.T) {
	ctx := context.Background()

	pgStore := proguard.NewMemoryStore()
	resolver := &proguard.Resolver{Store: pgStore}

	mapping := []byte(`
com.example.app.MainActivity -> a.b:
    42:42:void onCreate(android.os.Bundle) -> a
com.example.app.services.UserService -> c.d:
    88:88:java.lang.String getDisplayName() -> b
`)
	if err := pgStore.SaveMapping(ctx, &proguard.Mapping{
		ID:        "pg-1",
		ProjectID: "proj-android",
		ReleaseID: "com.example.app@3.2.1",
		UUID:      "debug-1",
		Name:      "mapping.txt",
	}, mapping); err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}

	proc := &Processor{
		Events:   store.NewMemoryEventStore(),
		Groups:   NewMemoryGroupStore(),
		Blobs:    store.NewMemoryBlobStore(),
		ProGuard: resolver,
	}

	payload := map[string]any{
		"platform": "java",
		"level":    "error",
		"release":  "com.example.app@3.2.1",
		"exception": map[string]any{
			"values": []map[string]any{
				{
					"type":  "java.lang.NullPointerException",
					"value": "boom",
					"stacktrace": map[string]any{
						"frames": []map[string]any{
							{
								"filename": "a.java",
								"function": "a",
								"module":   "a.b",
								"lineno":   42,
								"in_app":   true,
							},
						},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(payload)

	result, err := proc.Process(ctx, "proj-android", raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	evt, err := proc.Events.GetEvent(ctx, "proj-android", result.EventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}

	var normalizedEvent normalize.Event
	if err := json.Unmarshal(evt.NormalizedJSON, &normalizedEvent); err != nil {
		t.Fatalf("unmarshal normalized: %v", err)
	}

	frame := normalizedEvent.Exception.Values[0].Stacktrace.Frames[0]
	if frame.Module != "com.example.app.MainActivity" {
		t.Fatalf("Module = %q, want %q", frame.Module, "com.example.app.MainActivity")
	}
	if frame.Function != "onCreate" {
		t.Fatalf("Function = %q, want %q", frame.Function, "onCreate")
	}
	if frame.Filename != "MainActivity.java" {
		t.Fatalf("Filename = %q, want %q", frame.Filename, "MainActivity.java")
	}
	if frame.AbsPath != "a.b" {
		t.Fatalf("AbsPath = %q, want %q", frame.AbsPath, "a.b")
	}
}
