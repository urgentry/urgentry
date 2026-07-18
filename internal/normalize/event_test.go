package normalize

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExceptionList_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
		wantTyp string
		wantVal string
		wantErr bool
	}{
		{
			name:    "bare array form",
			input:   `[{"type":"testArrayForm","value":"array form"}]`,
			wantLen: 1,
			wantTyp: "testArrayForm",
			wantVal: "array form",
		},
		{
			name:    "object values form",
			input:   `{"values":[{"type":"testObjectForm","value":"object form"}]}`,
			wantLen: 1,
			wantTyp: "testObjectForm",
			wantVal: "object form",
		},
		{
			name:    "empty array",
			input:   `[]`,
			wantLen: 0,
		},
		{
			name:    "empty object",
			input:   `{}`,
			wantLen: 0,
		},
		{
			name:    "invalid string",
			input:   `"not-a-list"`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var list ExceptionList
			err := json.Unmarshal([]byte(tt.input), &list)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			if got := len(list.Values); got != tt.wantLen {
				t.Fatalf("len(Values) = %d, want %d", got, tt.wantLen)
			}
			if tt.wantLen > 0 {
				if list.Values[0].Type != tt.wantTyp {
					t.Errorf("Type = %q, want %q", list.Values[0].Type, tt.wantTyp)
				}
				if list.Values[0].Value != tt.wantVal {
					t.Errorf("Value = %q, want %q", list.Values[0].Value, tt.wantVal)
				}
			}
		})
	}
}

func TestNormalize_BareArrayException(t *testing.T) {
	// Repro from urgentry/urgentry#23 — sentry-go serializes exception as a bare array.
	raw := []byte(`{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1","platform":"go","level":"error","exception":[{"type":"testArrayForm","value":"array form"}]}`)
	evt, err := Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize bare-array exception: %v", err)
	}
	if evt.Exception == nil {
		t.Fatal("Exception is nil")
	}
	if len(evt.Exception.Values) != 1 {
		t.Fatalf("len(Exception.Values) = %d, want 1", len(evt.Exception.Values))
	}
	if evt.Exception.Values[0].Type != "testArrayForm" {
		t.Errorf("Type = %q, want testArrayForm", evt.Exception.Values[0].Type)
	}
	if evt.Exception.Values[0].Value != "array form" {
		t.Errorf("Value = %q, want array form", evt.Exception.Values[0].Value)
	}
}

func TestNormalize_ObjectFormException(t *testing.T) {
	raw := []byte(`{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2","platform":"go","level":"error","exception":{"values":[{"type":"testObjectForm","value":"object form"}]}}`)
	evt, err := Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize object-form exception: %v", err)
	}
	if evt.Exception == nil || len(evt.Exception.Values) != 1 {
		t.Fatalf("Exception = %+v, want one value", evt.Exception)
	}
	if evt.Exception.Values[0].Type != "testObjectForm" || evt.Exception.Values[0].Value != "object form" {
		t.Errorf("got type=%q value=%q", evt.Exception.Values[0].Type, evt.Exception.Values[0].Value)
	}
}

func TestNormalize_InvalidExceptionShape(t *testing.T) {
	raw := []byte(`{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3","platform":"go","level":"error","exception":"not-a-list"}`)
	_, err := Normalize(raw)
	if err == nil {
		t.Fatal("expected error for invalid exception shape")
	}
	if !strings.Contains(err.Error(), "invalid event JSON") {
		t.Errorf("error = %q, want invalid event JSON", err)
	}
}
