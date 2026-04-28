package id

import (
	"errors"
	"io"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestNewPanicsWhenRandomReaderFails(t *testing.T) {
	original := randomReader
	randomReader = failingReader{}
	defer func() {
		randomReader = original
		if r := recover(); r == nil {
			t.Fatal("New did not panic when randomness failed")
		}
	}()

	_ = New()
}

func TestNewReturnsThirtyTwoHexCharacters(t *testing.T) {
	original := randomReader
	randomReader = zeroReader{}
	defer func() { randomReader = original }()

	got := New()
	if len(got) != 32 {
		t.Fatalf("len(New()) = %d, want 32", len(got))
	}
	for _, ch := range got {
		if ch != '0' {
			t.Fatalf("New() = %q, want all zero hex from zeroReader", got)
		}
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), io.EOF
}
