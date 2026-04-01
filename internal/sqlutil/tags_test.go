package sqlutil

import "testing"

func TestParseTags_Object(t *testing.T) {
	raw := `{"environment":"production","browser":"Chrome"}`
	tags := ParseTags(raw)
	if tags["environment"] != "production" {
		t.Errorf("environment = %q, want %q", tags["environment"], "production")
	}
	if tags["browser"] != "Chrome" {
		t.Errorf("browser = %q, want %q", tags["browser"], "Chrome")
	}
}

func TestParseTags_Array(t *testing.T) {
	// ParseTags only handles JSON objects. An array of [key,val] pairs
	// should return an empty map (ParseTags does json.Unmarshal into map[string]string).
	raw := `[["key","val"]]`
	tags := ParseTags(raw)
	if len(tags) != 0 {
		t.Errorf("expected empty map for array input, got %v", tags)
	}
}

func TestParseTags_Empty(t *testing.T) {
	tags := ParseTags("")
	if tags == nil {
		t.Error("expected non-nil map for empty input")
	}
	if len(tags) != 0 {
		t.Errorf("expected empty map, got %v", tags)
	}
}

func TestParseTags_Invalid(t *testing.T) {
	tags := ParseTags("not json")
	if tags == nil {
		t.Error("expected non-nil map for invalid JSON")
	}
	if len(tags) != 0 {
		t.Errorf("expected empty map, got %v", tags)
	}
}
