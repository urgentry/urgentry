package profile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"urgentry/internal/sqlite"
)

var profileSink uint64
var profileBytesSink []byte

func TestRunWritesSummary(t *testing.T) {
	summary := runProfile(t, testConfig(t, "store-basic-error", KindNone, 2, "summary", 1))

	if summary.Scenario != "store-basic-error" {
		t.Fatalf("scenario = %q, want store-basic-error", summary.Scenario)
	}
	if summary.Kind != KindNone {
		t.Fatalf("kind = %q, want none", summary.Kind)
	}
	if summary.Events != 2 {
		t.Fatalf("events = %d, want 2", summary.Events)
	}
	if summary.Tables.Events != 2 {
		t.Fatalf("table events = %d, want 2", summary.Tables.Events)
	}
	if summary.Storage.DBBytes == 0 {
		t.Fatal("expected sqlite database bytes to be recorded")
	}
}

func TestRunWritesHeapProfile(t *testing.T) {
	summary := runProfile(t, testConfig(t, "envelope-single-error", KindHeap, 2, "heap", 1))
	assertProfilePath(t, summary.ProfilePath, "heap.pprof")
	if summary.Tables.Events != 2 {
		t.Fatalf("table events = %d, want 2", summary.Tables.Events)
	}
}

func TestRunWritesNonHeapProfiles(t *testing.T) {
	tests := []struct {
		name     string
		kind     Kind
		filename string
	}{
		{name: "cpu", kind: KindCPU, filename: "cpu.pprof"},
		{name: "allocs", kind: KindAllocs, filename: "allocs.pprof"},
		{name: "trace", kind: KindTrace, filename: "trace.out"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			summary := runProfile(t, testConfig(t, "envelope-single-error", tc.kind, 2, tc.name, 1))
			assertProfilePath(t, summary.ProfilePath, tc.filename)
			if summary.Events != 2 {
				t.Fatalf("events = %d, want 2", summary.Events)
			}
			if summary.Tables.Events != 2 {
				t.Fatalf("table events = %d, want 2", summary.Tables.Events)
			}
		})
	}
}

func TestRunRestoresGOMAXPROCS(t *testing.T) {
	baseline := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(baseline)

	target := 1
	if baseline == 1 {
		target = 2
	}

	summary := runProfile(t, testConfig(t, "store-basic-error", KindNone, 1, "gomaxprocs", target))

	if got := runtime.GOMAXPROCS(0); got != baseline {
		t.Fatalf("runtime.GOMAXPROCS = %d, want %d", got, baseline)
	}

	if summary.Runtime.GOMAXPROCS != target {
		t.Fatalf("summary runtime GOMAXPROCS = %d, want %d", summary.Runtime.GOMAXPROCS, target)
	}
}

func TestEnvironmentCloseReturnsDatabaseError(t *testing.T) {
	wantErr := errors.New("cleanup failed")
	env := &environment{
		cleanup: func() error { return wantErr },
	}
	if err := env.close(); !errors.Is(err, wantErr) {
		t.Fatalf("close error = %v, want %v", err, wantErr)
	}
}

func TestCloseEnvironmentJoinsErrors(t *testing.T) {
	closeErr := errors.New("cleanup failed")
	env := &environment{
		cleanup: func() error { return closeErr },
	}
	base := errors.New("scenario failed")
	joined := closeEnvironment(base, env)
	if !errors.Is(joined, base) || !errors.Is(joined, closeErr) {
		t.Fatalf("joined error = %v, want base + cleanup error", joined)
	}
}

func runProfile(t *testing.T, cfg Config) Summary {
	t.Helper()
	if err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return readSummary(t, filepath.Join(cfg.OutDir, "summary.json"))
}

func testConfig(t *testing.T, scenario string, kind Kind, iterations int, outDirName string, gomaxprocs int) Config {
	t.Helper()
	return Config{
		Scenario:   scenario,
		Kind:       kind,
		Iterations: iterations,
		OutDir:     filepath.Join(t.TempDir(), outDirName),
		ProjectID:  "proj-test",
		GOMAXPROCS: gomaxprocs,
	}
}

func assertProfilePath(t *testing.T, path, wantFilename string) {
	t.Helper()
	if path == "" {
		t.Fatal("expected profile path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat profile: %v", err)
	}
	if wantFilename != "" && filepath.Base(path) != wantFilename {
		t.Fatalf("profile file = %q, want %q", filepath.Base(path), wantFilename)
	}
}

func TestCollectTablesReturnsErrors(t *testing.T) {
	tests := []struct {
		name    string
		closeDB bool
		wantErr bool
	}{
		{name: "open database", closeDB: false},
		{name: "closed database", closeDB: true, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sqlite.Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if tc.closeDB {
				if err := db.Close(); err != nil {
					t.Fatalf("close db: %v", err)
				}
			}

			tables, err := collectTables(db)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("collectTables error = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("collectTables: %v", err)
			}
			if tables != (TableStats{}) {
				t.Fatalf("collectTables = %+v, want zero stats", tables)
			}
		})
	}
}

func TestStartProfilerWritesFiles(t *testing.T) {
	tests := []struct {
		name     string
		kind     Kind
		filename string
		work     func()
	}{
		{
			name:     "cpu",
			kind:     KindCPU,
			filename: "cpu.pprof",
			work:     burnCPU,
		},
		{
			name:     "allocs",
			kind:     KindAllocs,
			filename: "allocs.pprof",
			work: func() {
				profileBytesSink = make([]byte, 1<<20)
				runtime.KeepAlive(profileBytesSink)
			},
		},
		{
			name:     "trace",
			kind:     KindTrace,
			filename: "trace.out",
			work: func() {
				done := make(chan struct{})
				go func() {
					close(done)
				}()
				<-done
				runtime.Gosched()
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outDir := t.TempDir()
			profiler, err := startProfiler(tc.kind, outDir)
			if err != nil {
				t.Fatalf("startProfiler: %v", err)
			}
			tc.work()
			if err := profiler.stop(); err != nil {
				t.Fatalf("stop: %v", err)
			}

			info, err := os.Stat(filepath.Join(outDir, tc.filename))
			if err != nil {
				t.Fatalf("stat profile: %v", err)
			}
			if info.Size() == 0 {
				t.Fatalf("profile file %s is empty", tc.filename)
			}
		})
	}
}

func readSummary(t *testing.T, path string) Summary {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	var summary Summary
	if err := json.Unmarshal(body, &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	return summary
}

func burnCPU() {
	var sum uint64
	for i := 0; i < 5_000_000; i++ {
		sum += uint64(i % 7)
	}
	profileSink = sum
}
