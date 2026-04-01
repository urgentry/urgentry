package nativesym

import (
	"context"
	"testing"

	nativefixture "urgentry/internal/testfixtures/nativecrash"
)

func BenchmarkResolverResolveBreakpad(b *testing.B) {
	fixture := nativefixture.ByName(b, "apple_multimodule")
	if len(fixture.Symbols) == 0 {
		b.Fatal("apple fixture missing symbol source")
	}
	symbol := fixture.Symbols[0]
	resolver := NewResolver(&stubStore{
		debug: &File{ID: symbol.DebugID, Kind: symbol.Kind},
		body:  symbol.Body,
	})
	req := LookupRequest{
		ProjectID:       "bench-proj",
		ReleaseVersion:  fixture.Release,
		DebugID:         symbol.DebugID,
		InstructionAddr: "0x1010",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := resolver.Resolve(context.Background(), req)
		if err != nil {
			b.Fatalf("Resolve: %v", err)
		}
		if result.Status != LookupStatusResolved || result.Function != "main" {
			b.Fatalf("unexpected breakpad resolution: %+v", result)
		}
	}
}

func BenchmarkResolverResolveELF(b *testing.B) {
	fixture := nativefixture.ByName(b, "linux_elf")
	if len(fixture.Symbols) == 0 {
		b.Fatal("linux fixture missing symbol source")
	}
	symbol := fixture.Symbols[0]
	resolver := NewResolver(&stubStore{
		debug: &File{ID: symbol.CodeID, Kind: symbol.Kind},
		body:  symbol.Body,
	})
	req := LookupRequest{
		ProjectID:       "bench-proj",
		ReleaseVersion:  fixture.Release,
		CodeID:          symbol.CodeID,
		InstructionAddr: "0x1",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := resolver.Resolve(context.Background(), req)
		if err != nil {
			b.Fatalf("Resolve: %v", err)
		}
		if result.Status != LookupStatusResolved || result.Function != "handle_request" {
			b.Fatalf("unexpected ELF resolution: %+v", result)
		}
	}
}
