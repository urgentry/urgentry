package id

import (
	"encoding/hex"
	"testing"
)

func TestNew(t *testing.T) {
	first := New()
	second := New()

	if len(first) != 32 {
		t.Fatalf("len(first) = %d, want 32", len(first))
	}
	if len(second) != 32 {
		t.Fatalf("len(second) = %d, want 32", len(second))
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("first is not hex: %v", err)
	}
	if _, err := hex.DecodeString(second); err != nil {
		t.Fatalf("second is not hex: %v", err)
	}
	if first == second {
		t.Fatal("expected two generated ids to differ")
	}
}
