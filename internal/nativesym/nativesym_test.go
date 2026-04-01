package nativesym

import (
	"context"
	"errors"
	"testing"

	fixtures "urgentry/internal/testfixtures/nativesym"
)

type stubStore struct {
	debug *File
	body  []byte
}

func (s *stubStore) LookupByDebugID(_ context.Context, _, _, _, debugID string) (*File, []byte, error) {
	if s.debug != nil && s.debug.ID == debugID {
		return s.debug, s.body, nil
	}
	return nil, nil, nil
}

func (s *stubStore) LookupByCodeID(_ context.Context, _, _, _, codeID string) (*File, []byte, error) {
	if s.debug != nil && s.debug.ID == codeID {
		return s.debug, s.body, nil
	}
	return nil, nil, nil
}

func TestResolverResolveBreakpadSymbol(t *testing.T) {
	resolver := NewResolver(&stubStore{
		debug: &File{ID: "DEBUG1234", Kind: "macho"},
		body: []byte(`MODULE mac arm64 DEBUG1234 App
FILE 0 src/AppDelegate.swift
FUNC 1000 30 0 main
1000 10 41 0
1010 10 42 0
`),
	})

	result, err := resolver.Resolve(context.Background(), LookupRequest{
		ProjectID:       "proj-1",
		ReleaseVersion:  "ios@1.0.0",
		DebugID:         "DEBUG1234",
		InstructionAddr: "0x1015",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Status != LookupStatusResolved {
		t.Fatalf("status = %q, want resolved", result.Status)
	}
	if result.Module != "App" {
		t.Fatalf("module = %q, want App", result.Module)
	}
	if result.File != "src/AppDelegate.swift" {
		t.Fatalf("file = %q, want src/AppDelegate.swift", result.File)
	}
	if result.Function != "main" {
		t.Fatalf("function = %q, want main", result.Function)
	}
	if result.Line != 42 {
		t.Fatalf("line = %d, want 42", result.Line)
	}
}

func TestResolverResolveByCodeID(t *testing.T) {
	resolver := NewResolver(&stubStore{
		debug: &File{ID: "CODE1234", Kind: "macho"},
		body: []byte(`MODULE linux x86_64 CODE1234 server
FILE 0 src/server.c
FUNC 2000 20 0 handle_request
2000 20 88 0
`),
	})

	result, err := resolver.Resolve(context.Background(), LookupRequest{
		ProjectID:       "proj-1",
		ReleaseVersion:  "server@2.0.0",
		CodeID:          "CODE1234",
		InstructionAddr: "2005",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Status != LookupStatusResolved {
		t.Fatalf("status = %q, want resolved", result.Status)
	}
	if result.Module != "server" || result.File != "src/server.c" || result.Function != "handle_request" || result.Line != 88 {
		t.Fatalf("unexpected resolution: %+v", result)
	}
}

func TestResolverResolveELFSymbol(t *testing.T) {
	resolver := NewResolver(&stubStore{
		debug: &File{ID: "ELF-CODE-1", Kind: "elf"},
		body:  fixtures.ELFHandleRequestObject(t),
	})

	result, err := resolver.Resolve(context.Background(), LookupRequest{
		ProjectID:       "proj-1",
		ReleaseVersion:  "linux@1.0.0",
		CodeID:          "ELF-CODE-1",
		InstructionAddr: "0x1",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Status != LookupStatusResolved {
		t.Fatalf("status = %q, want resolved", result.Status)
	}
	if result.Format != "elf" || result.Function != "handle_request" {
		t.Fatalf("unexpected ELF resolution: %+v", result)
	}
}

func TestResolverELFSymbolMiss(t *testing.T) {
	resolver := NewResolver(&stubStore{
		debug: &File{ID: "ELF-CODE-1", Kind: "elf"},
		body:  fixtures.ELFHandleRequestObject(t),
	})

	result, err := resolver.Resolve(context.Background(), LookupRequest{
		ProjectID:       "proj-1",
		ReleaseVersion:  "linux@1.0.0",
		CodeID:          "ELF-CODE-1",
		InstructionAddr: "0x9999",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Status != LookupStatusMiss || result.Function != "" {
		t.Fatalf("unexpected ELF miss result: %+v", result)
	}
}

func TestResolverMalformedELFSymbolSource(t *testing.T) {
	resolver := NewResolver(&stubStore{
		debug: &File{ID: "ELF-CODE-1", Kind: "elf"},
		body:  []byte("not-an-elf"),
	})

	result, err := resolver.Resolve(context.Background(), LookupRequest{
		ProjectID:       "proj-1",
		ReleaseVersion:  "linux@1.0.0",
		CodeID:          "ELF-CODE-1",
		InstructionAddr: "0x1",
	})
	if !errors.Is(err, ErrMalformedSymbolSource) {
		t.Fatalf("err = %v, want malformed symbol source", err)
	}
	if result.Status != LookupStatusMalformed {
		t.Fatalf("status = %q, want malformed", result.Status)
	}
}
