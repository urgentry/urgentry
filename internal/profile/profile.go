package profile

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	rpprof "runtime/pprof"
	rtrace "runtime/trace"
	"time"

	"urgentry/internal/envelope"
	"urgentry/internal/issue"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	gtrace "urgentry/internal/trace"
)

type Kind string

const (
	KindNone   Kind = "none"
	KindCPU    Kind = "cpu"
	KindHeap   Kind = "heap"
	KindAllocs Kind = "allocs"
	KindTrace  Kind = "trace"
)

type Config struct {
	Scenario   string
	Kind       Kind
	Iterations int
	OutDir     string
	ProjectID  string
	GOMAXPROCS int
}

type Summary struct {
	Scenario     string        `json:"scenario"`
	Description  string        `json:"description"`
	Kind         Kind          `json:"kind"`
	Iterations   int           `json:"iterations"`
	ProjectID    string        `json:"project_id"`
	StartedAt    time.Time     `json:"started_at"`
	FinishedAt   time.Time     `json:"finished_at"`
	Duration     string        `json:"duration"`
	Events       int           `json:"events"`
	Transactions int           `json:"transactions"`
	ProfilePath  string        `json:"profile_path,omitempty"`
	DataDir      string        `json:"data_dir"`
	Storage      StorageStats  `json:"storage"`
	Tables       TableStats    `json:"tables"`
	Runtime      RuntimeStats  `json:"runtime"`
	Build        BuildMetadata `json:"build"`
	Command      []string      `json:"command"`
}

type StorageStats struct {
	DBBytes      int64 `json:"db_bytes"`
	WALBytes     int64 `json:"wal_bytes"`
	BlobBytes    int64 `json:"blob_bytes"`
	DataDirBytes int64 `json:"data_dir_bytes"`
}

type TableStats struct {
	Events       int64 `json:"events"`
	Groups       int64 `json:"groups"`
	Transactions int64 `json:"transactions"`
	Spans        int64 `json:"spans"`
	Releases     int64 `json:"releases"`
}

type RuntimeStats struct {
	GoVersion    string  `json:"go_version"`
	GOMAXPROCS   int     `json:"gomaxprocs"`
	NumCPU       int     `json:"num_cpu"`
	HeapAllocMB  float64 `json:"heap_alloc_mb"`
	TotalAllocMB float64 `json:"total_alloc_mb"`
	Mallocs      uint64  `json:"mallocs"`
	Frees        uint64  `json:"frees"`
	NumGC        uint32  `json:"num_gc"`
}

type BuildMetadata struct {
	MainPath    string `json:"main_path,omitempty"`
	Version     string `json:"version,omitempty"`
	VCSRevision string `json:"vcs_revision,omitempty"`
	VCSTime     string `json:"vcs_time,omitempty"`
	VCSModified string `json:"vcs_modified,omitempty"`
}

type scenario struct {
	name        string
	description string
	prepare     func(iterations int) ([][]byte, error)
	runPayload  func(ctx context.Context, env *environment, payload []byte) (resultCounts, error)
}

type resultCounts struct {
	events       int
	transactions int
}

type environment struct {
	dataDir   string
	projectID string
	db        *sql.DB
	processor *issue.Processor
	cleanup   func() error
}

func Run(cfg Config) error {
	if cfg.Iterations <= 0 {
		cfg.Iterations = 200
	}
	if cfg.ProjectID == "" {
		cfg.ProjectID = "profile-project"
	}
	if cfg.Kind == "" {
		cfg.Kind = KindNone
	}
	if !cfg.Kind.valid() {
		return fmt.Errorf("unsupported profile kind %q", cfg.Kind)
	}
	if cfg.OutDir == "" {
		return errors.New("out-dir is required")
	}
	activeGOMAXPROCS := runtime.GOMAXPROCS(0)
	if cfg.GOMAXPROCS > 0 {
		prev := runtime.GOMAXPROCS(cfg.GOMAXPROCS)
		activeGOMAXPROCS = cfg.GOMAXPROCS
		defer runtime.GOMAXPROCS(prev)
	}

	sc, err := lookupScenario(cfg.Scenario)
	if err != nil {
		return err
	}
	payloads, err := sc.prepare(cfg.Iterations)
	if err != nil {
		return fmt.Errorf("prepare scenario %s: %w", sc.name, err)
	}

	if err := os.RemoveAll(cfg.OutDir); err != nil {
		return fmt.Errorf("reset out dir: %w", err)
	}
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}

	dataDir := filepath.Join(cfg.OutDir, "data")
	env, err := newEnvironment(dataDir, cfg.ProjectID)
	if err != nil {
		return err
	}

	profiler, err := startProfiler(cfg.Kind, cfg.OutDir)
	if err != nil {
		return closeEnvironment(err, env)
	}
	startedAt := time.Now().UTC()
	counts, runErr := runScenario(context.Background(), env, sc, payloads)
	stopErr := profiler.stop()
	finishedAt := time.Now().UTC()
	if runErr != nil {
		return closeEnvironment(runErr, env)
	}
	if stopErr != nil {
		return closeEnvironment(stopErr, env)
	}

	summary, err := buildSummary(cfg, sc, env, counts, profiler.profilePath(), startedAt, finishedAt, activeGOMAXPROCS)
	if err != nil {
		return closeEnvironment(err, env)
	}
	return closeEnvironment(writeSummary(filepath.Join(cfg.OutDir, "summary.json"), summary), env)
}

func (k Kind) valid() bool {
	switch k {
	case KindNone, KindCPU, KindHeap, KindAllocs, KindTrace:
		return true
	default:
		return false
	}
}

func runScenario(ctx context.Context, env *environment, sc scenario, payloads [][]byte) (resultCounts, error) {
	var counts resultCounts
	for _, payload := range payloads {
		got, err := sc.runPayload(ctx, env, payload)
		if err != nil {
			return counts, fmt.Errorf("run scenario %s: %w", sc.name, err)
		}
		counts.events += got.events
		counts.transactions += got.transactions
	}
	return counts, nil
}

func lookupScenario(name string) (scenario, error) {
	for _, sc := range scenarios() {
		if sc.name == name {
			return sc, nil
		}
	}
	return scenario{}, fmt.Errorf("unsupported scenario %q", name)
}

func scenarios() []scenario {
	return []scenario{
		{
			name:        "store-basic-error",
			description: "Legacy store payload, fixed error fixture, real SQLite/blob writes.",
			prepare:     cloneJSONFixture("store", "basic_error.json"),
			runPayload:  runStorePayload,
		},
		{
			name:        "store-python-full",
			description: "Heavier store payload, fixed Python fixture, real SQLite/blob writes.",
			prepare:     cloneJSONFixture("store", "python_full_realistic.json"),
			runPayload:  runStorePayload,
		},
		{
			name:        "envelope-single-error",
			description: "Single error envelope parse plus event processing into SQLite/blob storage.",
			prepare:     cloneEnvelopeFixture("envelopes", "single_error.envelope"),
			runPayload:  runEnvelopePayload,
		},
		{
			name:        "otlp-trace",
			description: "OTLP trace translation plus transaction persistence into SQLite/blob storage.",
			prepare: func(iterations int) ([][]byte, error) {
				return cloneOTLPPayloads(iterations), nil
			},
			runPayload: runOTLPPayload,
		},
	}
}

func newEnvironment(dataDir, projectID string) (*environment, error) {
	db, err := sqlite.Open(dataDir)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	blobs, err := store.NewFileBlobStore(filepath.Join(dataDir, "blobs"))
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("create blob store: %w", err), closeErr)
		}
		return nil, fmt.Errorf("create blob store: %w", err)
	}
	env := &environment{
		dataDir:   dataDir,
		projectID: projectID,
		db:        db,
		processor: &issue.Processor{
			Events:   sqlite.NewEventStore(db),
			Groups:   sqlite.NewGroupStore(db),
			Blobs:    blobs,
			Releases: sqlite.NewReleaseStore(db),
			Traces:   sqlite.NewTraceStore(db),
		},
		cleanup: db.Close,
	}
	return env, nil
}

func (e *environment) close() error {
	if e.cleanup != nil {
		return e.cleanup()
	}
	return nil
}

func closeEnvironment(base error, env *environment) error {
	if env == nil {
		return base
	}
	closeErr := env.close()
	if base == nil {
		return closeErr
	}
	if closeErr == nil {
		return base
	}
	return errors.Join(base, closeErr)
}

func runStorePayload(ctx context.Context, env *environment, payload []byte) (resultCounts, error) {
	result, err := env.processor.Process(ctx, env.projectID, payload)
	if err != nil {
		return resultCounts{}, err
	}
	if result.EventType == "transaction" {
		return resultCounts{transactions: 1}, nil
	}
	return resultCounts{events: 1}, nil
}

func runEnvelopePayload(ctx context.Context, env *environment, payload []byte) (resultCounts, error) {
	envl, err := envelope.Parse(payload)
	if err != nil {
		return resultCounts{}, err
	}
	var counts resultCounts
	for _, item := range envl.Items {
		switch item.Header.Type {
		case "event", "transaction":
			result, err := env.processor.Process(ctx, env.projectID, item.Payload)
			if err != nil {
				return counts, err
			}
			if result.EventType == "transaction" {
				counts.transactions++
			} else {
				counts.events++
			}
		}
	}
	return counts, nil
}

func runOTLPPayload(ctx context.Context, env *environment, payload []byte) (resultCounts, error) {
	items, err := gtrace.TranslateOTLPJSON(payload)
	if err != nil {
		return resultCounts{}, err
	}
	var counts resultCounts
	for _, item := range items {
		result, err := env.processor.Process(ctx, env.projectID, item)
		if err != nil {
			return counts, err
		}
		if result.EventType == "transaction" {
			counts.transactions++
		} else {
			counts.events++
		}
	}
	return counts, nil
}

func cloneJSONFixture(subdir, name string) func(iterations int) ([][]byte, error) {
	return func(iterations int) ([][]byte, error) {
		base, err := loadFixture(subdir, name)
		if err != nil {
			return nil, err
		}
		return cloneJSONPayloads(base, iterations), nil
	}
}

func cloneEnvelopeFixture(subdir, name string) func(iterations int) ([][]byte, error) {
	return func(iterations int) ([][]byte, error) {
		base, err := loadFixture(subdir, name)
		if err != nil {
			return nil, err
		}
		return cloneEnvelopePayloads(base, iterations)
	}
}

func buildSummary(cfg Config, sc scenario, env *environment, counts resultCounts, profilePath string, startedAt, finishedAt time.Time, gomaxprocs int) (Summary, error) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	storage, err := collectStorage(env.dataDir)
	if err != nil {
		return Summary{}, err
	}
	tables, err := collectTables(env.db)
	if err != nil {
		return Summary{}, err
	}
	absDataDir, err := filepath.Abs(env.dataDir)
	if err != nil {
		return Summary{}, err
	}
	absProfile := ""
	if profilePath != "" {
		absProfile, err = filepath.Abs(profilePath)
		if err != nil {
			return Summary{}, err
		}
	}
	return Summary{
		Scenario:     sc.name,
		Description:  sc.description,
		Kind:         cfg.Kind,
		Iterations:   cfg.Iterations,
		ProjectID:    cfg.ProjectID,
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
		Duration:     finishedAt.Sub(startedAt).String(),
		Events:       counts.events,
		Transactions: counts.transactions,
		ProfilePath:  absProfile,
		DataDir:      absDataDir,
		Storage:      storage,
		Tables:       tables,
		Runtime: RuntimeStats{
			GoVersion:    runtime.Version(),
			GOMAXPROCS:   gomaxprocs,
			NumCPU:       runtime.NumCPU(),
			HeapAllocMB:  bytesToMB(mem.HeapAlloc),
			TotalAllocMB: bytesToMB(mem.TotalAlloc),
			Mallocs:      mem.Mallocs,
			Frees:        mem.Frees,
			NumGC:        mem.NumGC,
		},
		Build:   readBuildMetadata(),
		Command: append([]string(nil), os.Args...),
	}, nil
}

func collectStorage(dataDir string) (StorageStats, error) {
	dbPath := filepath.Join(dataDir, "urgentry.db")
	walPath := filepath.Join(dataDir, "urgentry.db-wal")
	blobPath := filepath.Join(dataDir, "blobs")
	dataBytes, err := dirSize(dataDir)
	if err != nil {
		return StorageStats{}, err
	}
	blobBytes, err := dirSize(blobPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return StorageStats{}, err
	}
	return StorageStats{
		DBBytes:      fileSize(dbPath),
		WALBytes:     fileSize(walPath),
		BlobBytes:    blobBytes,
		DataDirBytes: dataBytes,
	}, nil
}

func collectTables(db *sql.DB) (TableStats, error) {
	events, err := countRows(db, "events")
	if err != nil {
		return TableStats{}, err
	}
	groups, err := countRows(db, "groups")
	if err != nil {
		return TableStats{}, err
	}
	transactions, err := countRows(db, "transactions")
	if err != nil {
		return TableStats{}, err
	}
	spans, err := countRows(db, "spans")
	if err != nil {
		return TableStats{}, err
	}
	releases, err := countRows(db, "releases")
	if err != nil {
		return TableStats{}, err
	}
	return TableStats{
		Events:       events,
		Groups:       groups,
		Transactions: transactions,
		Spans:        spans,
		Releases:     releases,
	}, nil
}

func countRows(db *sql.DB, table string) (int64, error) {
	var count int64
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		return 0, fmt.Errorf("count %s rows: %w", table, err)
	}
	return count, nil
}

func writeSummary(path string, summary Summary) error {
	body, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

type profiler struct {
	kind   Kind
	path   string
	stopFn func() error
}

func startProfiler(kind Kind, outDir string) (*profiler, error) {
	switch kind {
	case KindNone:
		return &profiler{kind: kind, stopFn: func() error { return nil }}, nil
	case KindCPU:
		path := filepath.Join(outDir, "cpu.pprof")
		file, err := os.Create(path)
		if err != nil {
			return nil, fmt.Errorf("create cpu profile: %w", err)
		}
		if err := rpprof.StartCPUProfile(file); err != nil {
			file.Close()
			return nil, fmt.Errorf("start cpu profile: %w", err)
		}
		return &profiler{
			kind: kind,
			path: path,
			stopFn: func() error {
				rpprof.StopCPUProfile()
				return file.Close()
			},
		}, nil
	case KindHeap:
		path := filepath.Join(outDir, "heap.pprof")
		return &profiler{
			kind: kind,
			path: path,
			stopFn: func() error {
				runtime.GC()
				return writeRuntimeProfile(path, "heap")
			},
		}, nil
	case KindAllocs:
		path := filepath.Join(outDir, "allocs.pprof")
		return &profiler{
			kind: kind,
			path: path,
			stopFn: func() error {
				return writeRuntimeProfile(path, "allocs")
			},
		}, nil
	case KindTrace:
		path := filepath.Join(outDir, "trace.out")
		file, err := os.Create(path)
		if err != nil {
			return nil, fmt.Errorf("create trace file: %w", err)
		}
		if err := rtrace.Start(file); err != nil {
			file.Close()
			return nil, fmt.Errorf("start trace: %w", err)
		}
		return &profiler{
			kind: kind,
			path: path,
			stopFn: func() error {
				rtrace.Stop()
				return file.Close()
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported profile kind %q", kind)
	}
}

func (p *profiler) stop() error {
	if p == nil || p.stopFn == nil {
		return nil
	}
	return p.stopFn()
}

func (p *profiler) profilePath() string {
	if p == nil {
		return ""
	}
	return p.path
}

func writeRuntimeProfile(path, name string) error {
	prof := rpprof.Lookup(name)
	if prof == nil {
		return fmt.Errorf("runtime profile %q is unavailable", name)
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return prof.WriteTo(file, 0)
}

func loadFixture(subdir, name string) ([]byte, error) {
	root, err := fixturesRoot()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(root, subdir, name))
}

func fixturesRoot() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot determine fixture root")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "eval", "fixtures"), nil
}

func cloneJSONPayloads(base []byte, iterations int) [][]byte {
	payloads := make([][]byte, 0, iterations)
	for i := 0; i < iterations; i++ {
		payloads = append(payloads, rewriteEventIDJSON(base, stableEventID(i)))
	}
	return payloads
}

func cloneEnvelopePayloads(base []byte, iterations int) ([][]byte, error) {
	payloads := make([][]byte, 0, iterations)
	for i := 0; i < iterations; i++ {
		cloned, err := rewriteEnvelopeEventID(base, stableEventID(i))
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, cloned)
	}
	return payloads, nil
}

func cloneOTLPPayloads(iterations int) [][]byte {
	payloads := make([][]byte, 0, iterations)
	for i := 0; i < iterations; i++ {
		traceID := stableTraceID(i)
		spanID := stableSpanID(i)
		payload := bytes.ReplaceAll(baseOTLPTracePayload, []byte("0102030405060708090a0b0c0d0e0f10"), []byte(traceID))
		payload = bytes.ReplaceAll(payload, []byte("1111111111111111"), []byte(spanID))
		payloads = append(payloads, payload)
	}
	return payloads
}

func rewriteEventIDJSON(base []byte, eventID string) []byte {
	var doc map[string]any
	if err := json.Unmarshal(base, &doc); err != nil {
		return append([]byte(nil), base...)
	}
	doc["event_id"] = eventID
	out, err := json.Marshal(doc)
	if err != nil {
		return append([]byte(nil), base...)
	}
	return out
}

func rewriteEnvelopeEventID(base []byte, eventID string) ([]byte, error) {
	envl, err := envelope.Parse(base)
	if err != nil {
		return nil, err
	}
	envl.Header.EventID = eventID
	for i := range envl.Items {
		switch envl.Items[i].Header.Type {
		case "event", "transaction", "user_report":
			envl.Items[i].Payload = rewriteEventIDJSON(envl.Items[i].Payload, eventID)
			envl.Items[i].Header.Length = len(envl.Items[i].Payload)
		}
	}
	return marshalEnvelope(envl)
}

func marshalEnvelope(envl *envelope.Envelope) ([]byte, error) {
	var buf bytes.Buffer
	header, err := json.Marshal(envl.Header)
	if err != nil {
		return nil, err
	}
	buf.Write(header)
	buf.WriteByte('\n')
	for _, item := range envl.Items {
		header, err := json.Marshal(item.Header)
		if err != nil {
			return nil, err
		}
		buf.Write(header)
		buf.WriteByte('\n')
		buf.Write(item.Payload)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func stableEventID(index int) string {
	return fmt.Sprintf("%032x", index+1)
}

func stableTraceID(index int) string {
	return fmt.Sprintf("%032x", index+1)
}

func stableSpanID(index int) string {
	return fmt.Sprintf("%016x", index+1)
}

func readBuildMetadata() BuildMetadata {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return BuildMetadata{}
	}
	meta := BuildMetadata{
		MainPath: info.Path,
		Version:  info.Main.Version,
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			meta.VCSRevision = setting.Value
		case "vcs.time":
			meta.VCSTime = setting.Value
		case "vcs.modified":
			meta.VCSModified = setting.Value
		}
	}
	return meta
}

func bytesToMB(v uint64) float64 {
	return float64(v) / 1024 / 1024
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

var baseOTLPTracePayload = []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeSpans":[{"spans":[{"traceId":"0102030405060708090a0b0c0d0e0f10","spanId":"1111111111111111","name":"GET /checkout","kind":2,"startTimeUnixNano":"1743076800000000000","endTimeUnixNano":"1743076801000000000","attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}}],"status":{"code":1}}]}]}]}`)
