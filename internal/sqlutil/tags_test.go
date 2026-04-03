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
	if len(tags) != 0 {
		t.Errorf("expected nil/empty for empty input, got %v", tags)
	}
}

func TestParseTags_EmptyObject(t *testing.T) {
	tags := ParseTags("{}")
	if len(tags) != 0 {
		t.Errorf("expected nil/empty for {}, got %v", tags)
	}
}

func TestParseTags_Invalid(t *testing.T) {
	tags := ParseTags("not json")
	if len(tags) != 0 {
		t.Errorf("expected nil/empty for invalid JSON, got %v", tags)
	}
}
