package normalize

import "testing"

// ---------------------------------------------------------------------------
// ParseFrames
// ---------------------------------------------------------------------------

func TestParseFrames(t *testing.T) {
	raw := `{
		"exception": {
			"values": [{
				"type": "Error",
				"value": "test error",
				"stacktrace": {
					"frames": [
						{"filename": "net/http/server.go", "function": "ServeHTTP", "lineno": 2936, "in_app": false},
						{"filename": "app/handler.go", "function": "handleRequest", "lineno": 42, "colno": 10, "in_app": true, "context_line": "    result, err := process(ctx)"}
					]
				}
			}]
		}
	}`
	frames := ParseFrames(raw)
	if len(frames) == 0 {
		t.Fatal("expected frames, got none")
	}
	// Frames are reversed (most recent call first).
	if frames[0].File != "app/handler.go" {
		t.Errorf("first frame file = %q, want %q", frames[0].File, "app/handler.go")
	}
	if frames[0].Function != "handleRequest" {
		t.Errorf("first frame function = %q, want %q", frames[0].Function, "handleRequest")
	}
	if frames[0].LineNo != 42 {
		t.Errorf("first frame lineno = %d, want 42", frames[0].LineNo)
	}
	if frames[0].ColNo != 10 {
		t.Errorf("first frame colno = %d, want 10", frames[0].ColNo)
	}
	if !frames[0].InApp {
		t.Error("first frame should be in_app")
	}
	if len(frames[0].CodeLines) == 0 {
		t.Error("expected code context lines for frame with context_line")
	}
	if frames[1].File != "net/http/server.go" {
		t.Errorf("second frame file = %q, want %q", frames[1].File, "net/http/server.go")
	}
}

func TestParseFrames_Empty(t *testing.T) {
	// No exception key.
	frames := ParseFrames(`{"tags": {"env": "prod"}}`)
	if frames != nil {
		t.Errorf("expected nil frames, got %d", len(frames))
	}

	// Empty string.
	frames = ParseFrames("")
	if frames != nil {
		t.Errorf("expected nil frames for empty input, got %d", len(frames))
	}
}

// ---------------------------------------------------------------------------
// ParseUser
// ---------------------------------------------------------------------------

func TestParseUser(t *testing.T) {
	raw := `{
		"user": {
			"id": "42",
			"email": "test@example.com",
			"username": "testuser",
			"ip_address": "10.0.0.1"
		}
	}`
	pairs := ParseUser(raw)
	if len(pairs) != 4 {
		t.Fatalf("expected 4 pairs, got %d", len(pairs))
	}
	found := make(map[string]string)
	for _, p := range pairs {
		found[p.Key] = p.Value
	}
	if found["ID"] != "42" {
		t.Errorf("ID = %q, want %q", found["ID"], "42")
	}
	if found["Email"] != "test@example.com" {
		t.Errorf("Email = %q, want %q", found["Email"], "test@example.com")
	}
	if found["Username"] != "testuser" {
		t.Errorf("Username = %q, want %q", found["Username"], "testuser")
	}
	if found["IP Address"] != "10.0.0.1" {
		t.Errorf("IP Address = %q, want %q", found["IP Address"], "10.0.0.1")
	}
}

// ---------------------------------------------------------------------------
// ParseRequest
// ---------------------------------------------------------------------------

func TestParseRequest(t *testing.T) {
	raw := `{
		"request": {
			"method": "POST",
			"url": "https://api.example.com/data",
			"headers": {"Host": "api.example.com"}
		}
	}`
	pairs := ParseRequest(raw)
	if len(pairs) != 3 {
		t.Fatalf("expected 3 pairs, got %d", len(pairs))
	}
	found := make(map[string]string)
	for _, p := range pairs {
		found[p.Key] = p.Value
	}
	if found["Method"] != "POST" {
		t.Errorf("Method = %q, want %q", found["Method"], "POST")
	}
	if found["URL"] != "https://api.example.com/data" {
		t.Errorf("URL = %q, want %q", found["URL"], "https://api.example.com/data")
	}
	if found["Host"] != "api.example.com" {
		t.Errorf("Host = %q, want %q", found["Host"], "api.example.com")
	}
}

// ---------------------------------------------------------------------------
// ParseBreadcrumbs
// ---------------------------------------------------------------------------

func TestParseBreadcrumbs(t *testing.T) {
	raw := `{
		"breadcrumbs": {
			"values": [
				{"timestamp": "2024-01-15T10:30:00Z", "category": "http", "message": "GET /api", "level": "info"},
				{"timestamp": 1705312200.0, "category": "query", "message": "SELECT *", "level": "debug"}
			]
		}
	}`
	bcs := ParseBreadcrumbs(raw)
	if len(bcs) != 2 {
		t.Fatalf("expected 2 breadcrumbs, got %d", len(bcs))
	}
	if bcs[0].Category != "http" {
		t.Errorf("bc[0].Category = %q, want %q", bcs[0].Category, "http")
	}
	if bcs[0].Message != "GET /api" {
		t.Errorf("bc[0].Message = %q, want %q", bcs[0].Message, "GET /api")
	}
	if bcs[0].Level != "info" {
		t.Errorf("bc[0].Level = %q, want %q", bcs[0].Level, "info")
	}
	if bcs[0].Time != "10:30:00" {
		t.Errorf("bc[0].Time = %q, want %q", bcs[0].Time, "10:30:00")
	}
	if bcs[1].Category != "query" {
		t.Errorf("bc[1].Category = %q, want %q", bcs[1].Category, "query")
	}
}

// ---------------------------------------------------------------------------
// ParseNormalizedTags
// ---------------------------------------------------------------------------

func TestParseNormalizedTags(t *testing.T) {
	// Tags as a map.
	raw := `{"tags": {"environment": "production", "level": "error"}}`
	pairs := ParseNormalizedTags(raw)
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}
	found := make(map[string]string)
	for _, p := range pairs {
		found[p.Key] = p.Value
	}
	if found["environment"] != "production" {
		t.Errorf("environment = %q, want %q", found["environment"], "production")
	}
	if found["level"] != "error" {
		t.Errorf("level = %q, want %q", found["level"], "error")
	}
}

func TestParseNormalizedTags_Array(t *testing.T) {
	// Tags as array of [key, value] pairs.
	raw := `{"tags": [["browser", "Chrome"], ["os", "Linux"]]}`
	pairs := ParseNormalizedTags(raw)
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}
	found := make(map[string]string)
	for _, p := range pairs {
		found[p.Key] = p.Value
	}
	if found["browser"] != "Chrome" {
		t.Errorf("browser = %q, want %q", found["browser"], "Chrome")
	}
	if found["os"] != "Linux" {
		t.Errorf("os = %q, want %q", found["os"], "Linux")
	}
}

func TestParseNormalizedTags_ObjectArray(t *testing.T) {
	raw := `{"tags":[{"key":"service.name","value":"order-api"},{"key":"environment","value":"production"}]}`
	pairs := ParseNormalizedTags(raw)
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}
	found := make(map[string]string)
	for _, p := range pairs {
		found[p.Key] = p.Value
	}
	if found["service.name"] != "order-api" {
		t.Errorf("service.name = %q, want order-api", found["service.name"])
	}
	if found["environment"] != "production" {
		t.Errorf("environment = %q, want production", found["environment"])
	}
}

func TestParseNormalizedTags_Empty(t *testing.T) {
	pairs := ParseNormalizedTags("")
	if pairs != nil {
		t.Errorf("expected nil for empty input, got %d pairs", len(pairs))
	}

	pairs = ParseNormalizedTags(`{"exception": {}}`)
	if pairs != nil {
		t.Errorf("expected nil for JSON without tags, got %d pairs", len(pairs))
	}
}
