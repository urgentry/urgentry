package web

import (
	"testing"
)

func TestParseContextPanels_AllKnownTypes(t *testing.T) {
	payload := `{
		"contexts": {
			"device": {"name": "iPhone 14", "model": "iPhone14,5", "arch": "arm64", "memory_size": 6144, "simulator": false},
			"os": {"name": "iOS", "version": "17.2", "build": "21C62"},
			"browser": {"name": "Chrome", "version": "120.0.6099.199"},
			"runtime": {"name": "CPython", "version": "3.12.1"},
			"app": {"app_name": "MyApp", "app_version": "2.1.0", "app_build": "1042"},
			"gpu": {"name": "Apple GPU", "vendor_name": "Apple"}
		}
	}`

	panels := parseContextPanels(payload)
	if len(panels) != 6 {
		t.Fatalf("expected 6 panels, got %d", len(panels))
	}

	// Verify ordering matches knownContextKeys.
	expectedTitles := []string{"Device", "Operating System", "Browser", "Runtime", "App", "GPU"}
	for i, want := range expectedTitles {
		if panels[i].Title != want {
			t.Errorf("panels[%d].Title = %q, want %q", i, panels[i].Title, want)
		}
	}

	// Spot-check device items.
	dev := panels[0]
	assertItem(t, dev.Items, "Name", "iPhone 14")
	assertItem(t, dev.Items, "Model", "iPhone14,5")
	assertItem(t, dev.Items, "Arch", "arm64")
	assertItem(t, dev.Items, "Memory Size", "6144")
	assertItem(t, dev.Items, "Simulator", "no")

	// Spot-check OS items.
	os := panels[1]
	assertItem(t, os.Items, "Name", "iOS")
	assertItem(t, os.Items, "Version", "17.2")
	assertItem(t, os.Items, "Build", "21C62")
}

func TestParseContextPanels_UnknownTypes(t *testing.T) {
	payload := `{
		"contexts": {
			"custom_ctx": {"foo": "bar", "count": 42},
			"trace": {"trace_id": "abc123", "span_id": "def456"}
		}
	}`

	panels := parseContextPanels(payload)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel (trace excluded), got %d", len(panels))
	}
	if panels[0].Title != "Custom Ctx" {
		t.Errorf("Title = %q, want %q", panels[0].Title, "Custom Ctx")
	}
	assertItem(t, panels[0].Items, "Count", "42")
	assertItem(t, panels[0].Items, "Foo", "bar")
}

func TestParseContextPanels_Empty(t *testing.T) {
	if panels := parseContextPanels(""); panels != nil {
		t.Errorf("expected nil for empty input, got %v", panels)
	}
	if panels := parseContextPanels(`{}`); panels != nil {
		t.Errorf("expected nil for no contexts, got %v", panels)
	}
	if panels := parseContextPanels(`not json`); panels != nil {
		t.Errorf("expected nil for invalid JSON, got %v", panels)
	}
}

func TestParseContextPanels_TypeMetaKeyStripped(t *testing.T) {
	payload := `{
		"contexts": {
			"os": {"type": "os", "name": "Linux", "version": "6.1"}
		}
	}`

	panels := parseContextPanels(payload)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	for _, item := range panels[0].Items {
		if item.Key == "Type" {
			t.Error("'type' meta-key should be stripped from context items")
		}
	}
	assertItem(t, panels[0].Items, "Name", "Linux")
}

func TestParseContextPanels_BoolValues(t *testing.T) {
	payload := `{
		"contexts": {
			"device": {"simulator": true, "online": false}
		}
	}`

	panels := parseContextPanels(payload)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	assertItem(t, panels[0].Items, "Simulator", "yes")
	assertItem(t, panels[0].Items, "Online", "no")
}

func TestParseContextPanels_FloatValues(t *testing.T) {
	payload := `{
		"contexts": {
			"device": {"battery_level": 85.50}
		}
	}`

	panels := parseContextPanels(payload)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	assertItem(t, panels[0].Items, "Battery Level", "85.50")
}

func TestParseContextPanels_NilValues(t *testing.T) {
	payload := `{
		"contexts": {
			"device": {"name": null, "model": "iPhone14,5"}
		}
	}`

	panels := parseContextPanels(payload)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	// null values should be skipped.
	if len(panels[0].Items) != 1 {
		t.Fatalf("expected 1 item (null skipped), got %d", len(panels[0].Items))
	}
	assertItem(t, panels[0].Items, "Model", "iPhone14,5")
}

func TestContextLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"memory_size", "Memory Size"},
		{"name", "Name"},
		{"ip_address", "Ip Address"},
		{"", ""},
		{"multi_threaded_rendering", "Multi Threaded Rendering"},
	}
	for _, tt := range tests {
		got := contextLabel(tt.input)
		if got != tt.want {
			t.Errorf("contextLabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatContextValue(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string
	}{
		{"string", "hello", "hello"},
		{"bool true", true, "yes"},
		{"bool false", false, "no"},
		{"int float", float64(42), "42"},
		{"decimal float", float64(3.14), "3.14"},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatContextValue(tt.val)
			if got != tt.want {
				t.Errorf("formatContextValue(%v) = %q, want %q", tt.val, got, tt.want)
			}
		})
	}
}

func assertItem(t *testing.T, items []kvPair, key, wantValue string) {
	t.Helper()
	for _, item := range items {
		if item.Key == key {
			if item.Value != wantValue {
				t.Errorf("item %q = %q, want %q", key, item.Value, wantValue)
			}
			return
		}
	}
	t.Errorf("item %q not found in items", key)
}
