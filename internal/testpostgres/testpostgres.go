package testpostgres

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Provider owns one ephemeral postgres cluster per test package and hands out
// isolated databases per test/benchmark.
//
// Flow:
//
//	provider.Once -> start cluster
//	              -> OpenDatabase()
//	                 -> CREATE DATABASE unique_name
//	                 -> run test against dedicated DB
//	                 -> close DB
//	                 -> terminate sessions + DROP DATABASE
type Provider struct {
	name    string
	once    sync.Once
	closeMu sync.Mutex
	cluster cluster
	nextID  atomic.Uint64
}

type Template struct {
	provider *Provider
	name     string
	prepare  func(*sql.DB) error

	once   sync.Once
	dbName string
	err    error
}

type cluster struct {
	dsn  string
	stop func()
	err  error
}

func NewProvider(name string) *Provider {
	return &Provider{name: sanitizeIdentifier(name)}
}

func (p *Provider) NewTemplate(name string, prepare func(*sql.DB) error) *Template {
	return &Template{
		provider: p,
		name:     sanitizeIdentifier(name),
		prepare:  prepare,
	}
}

func (p *Provider) OpenDatabase(tb testing.TB, prefix string) *sql.DB {
	db, _ := p.OpenDatabaseWithDSN(tb, prefix)
	return db
}

func (p *Provider) OpenDatabaseWithDSN(tb testing.TB, prefix string) (*sql.DB, string) {
	tb.Helper()

	cluster := p.testCluster(tb)
	root, err := sql.Open("pgx", cluster.dsn)
	if err != nil {
		tb.Fatalf("open root postgres: %v", err)
	}

	dbName := p.nextDatabaseName(prefix)
	if _, err := root.Exec(`CREATE DATABASE ` + dbName); err != nil {
		_ = root.Close()
		tb.Fatalf("create test database: %v", err)
	}

	testDSN, err := replaceDatabaseName(cluster.dsn, dbName)
	if err != nil {
		_, _ = root.Exec(`DROP DATABASE ` + dbName)
		_ = root.Close()
		tb.Fatalf("build test DSN: %v", err)
	}
	db, err := sql.Open("pgx", testDSN)
	if err != nil {
		_, _ = root.Exec(`DROP DATABASE ` + dbName)
		_ = root.Close()
		tb.Fatalf("open test database: %v", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		_, _ = root.Exec(`DROP DATABASE ` + dbName)
		_ = root.Close()
		tb.Fatalf("ping test database: %v", err)
	}

	tb.Cleanup(func() {
		_ = db.Close()
		_, _ = root.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		_, _ = root.Exec(`DROP DATABASE IF EXISTS ` + dbName)
		_ = root.Close()
	})
	return db, testDSN
}

func (t *Template) OpenDatabase(tb testing.TB, prefix string) *sql.DB {
	db, _ := t.OpenDatabaseWithDSN(tb, prefix)
	return db
}

// OpenPersistentDatabaseWithDSN opens a database cloned from the template
// without registering test cleanup. Callers that need a process-wide fixture,
// such as benchmark harnesses, are responsible for invoking the returned
// cleanup function when appropriate.
func (t *Template) OpenPersistentDatabaseWithDSN(tb testing.TB, prefix string) (*sql.DB, string, func(), error) {
	tb.Helper()

	if t == nil || t.provider == nil {
		return nil, "", nil, fmt.Errorf("testpostgres template is nil")
	}
	t.ensure(tb)
	if t.err != nil {
		return nil, "", nil, fmt.Errorf("initialize test template %q: %w", t.name, t.err)
	}

	cluster := t.provider.testCluster(tb)
	root, err := sql.Open("pgx", cluster.dsn)
	if err != nil {
		return nil, "", nil, fmt.Errorf("open root postgres: %w", err)
	}

	dbName := t.provider.nextDatabaseName(prefix)
	if _, err := root.Exec(`CREATE DATABASE ` + dbName + ` TEMPLATE ` + t.dbName); err != nil {
		_ = root.Close()
		return nil, "", nil, fmt.Errorf("create test database from template: %w", err)
	}

	testDSN, err := replaceDatabaseName(cluster.dsn, dbName)
	if err != nil {
		_, _ = root.Exec(`DROP DATABASE ` + dbName)
		_ = root.Close()
		return nil, "", nil, fmt.Errorf("build test DSN: %w", err)
	}
	db, err := sql.Open("pgx", testDSN)
	if err != nil {
		_, _ = root.Exec(`DROP DATABASE ` + dbName)
		_ = root.Close()
		return nil, "", nil, fmt.Errorf("open test database: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		_, _ = root.Exec(`DROP DATABASE ` + dbName)
		_ = root.Close()
		return nil, "", nil, fmt.Errorf("ping test database: %w", err)
	}

	cleanup := func() {
		_ = db.Close()
		_, _ = root.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		_, _ = root.Exec(`DROP DATABASE IF EXISTS ` + dbName)
		_ = root.Close()
	}
	return db, testDSN, cleanup, nil
}

// OpenPersistentDatabase is like OpenPersistentDatabaseWithDSN but returns only
// the opened database and cleanup function.
func (t *Template) OpenPersistentDatabase(tb testing.TB, prefix string) (*sql.DB, func(), error) {
	db, _, cleanup, err := t.OpenPersistentDatabaseWithDSN(tb, prefix)
	return db, cleanup, err
}

func (t *Template) OpenDatabaseWithDSN(tb testing.TB, prefix string) (*sql.DB, string) {
	tb.Helper()

	if t == nil || t.provider == nil {
		tb.Fatal("testpostgres template is nil")
	}
	t.ensure(tb)
	if t.err != nil {
		tb.Fatalf("initialize test template %q: %v", t.name, t.err)
	}

	cluster := t.provider.testCluster(tb)
	root, err := sql.Open("pgx", cluster.dsn)
	if err != nil {
		tb.Fatalf("open root postgres: %v", err)
	}

	dbName := t.provider.nextDatabaseName(prefix)
	if _, err := root.Exec(`CREATE DATABASE ` + dbName + ` TEMPLATE ` + t.dbName); err != nil {
		_ = root.Close()
		tb.Fatalf("create test database from template: %v", err)
	}

	testDSN, err := replaceDatabaseName(cluster.dsn, dbName)
	if err != nil {
		_, _ = root.Exec(`DROP DATABASE ` + dbName)
		_ = root.Close()
		tb.Fatalf("build test DSN: %v", err)
	}
	db, err := sql.Open("pgx", testDSN)
	if err != nil {
		_, _ = root.Exec(`DROP DATABASE ` + dbName)
		_ = root.Close()
		tb.Fatalf("open test database: %v", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		_, _ = root.Exec(`DROP DATABASE ` + dbName)
		_ = root.Close()
		tb.Fatalf("ping test database: %v", err)
	}

	tb.Cleanup(func() {
		_ = db.Close()
		_, _ = root.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		_, _ = root.Exec(`DROP DATABASE IF EXISTS ` + dbName)
		_ = root.Close()
	})
	return db, testDSN
}

func (p *Provider) Close() {
	p.closeMu.Lock()
	defer p.closeMu.Unlock()
	if p.cluster.stop == nil {
		return
	}
	p.cluster.stop()
	p.cluster.stop = nil
}

func (t *Template) ensure(tb testing.TB) {
	tb.Helper()

	t.once.Do(func() {
		cluster := t.provider.testCluster(tb)
		root, err := sql.Open("pgx", cluster.dsn)
		if err != nil {
			t.err = fmt.Errorf("open root postgres: %w", err)
			return
		}
		defer root.Close()

		name := t.name
		if name == "" {
			name = "prepared"
		}
		dbName := t.provider.nextDatabaseName(name + "_template")
		if _, err := root.Exec(`CREATE DATABASE ` + dbName); err != nil {
			t.err = fmt.Errorf("create template database: %w", err)
			return
		}
		templateDSN, err := replaceDatabaseName(cluster.dsn, dbName)
		if err != nil {
			_, _ = root.Exec(`DROP DATABASE ` + dbName)
			t.err = fmt.Errorf("build template DSN: %w", err)
			return
		}
		db, err := sql.Open("pgx", templateDSN)
		if err != nil {
			_, _ = root.Exec(`DROP DATABASE ` + dbName)
			t.err = fmt.Errorf("open template database: %w", err)
			return
		}
		if err := db.Ping(); err != nil {
			_ = db.Close()
			_, _ = root.Exec(`DROP DATABASE ` + dbName)
			t.err = fmt.Errorf("ping template database: %w", err)
			return
		}
		if t.prepare != nil {
			if err := t.prepare(db); err != nil {
				_ = db.Close()
				_, _ = root.Exec(`DROP DATABASE ` + dbName)
				t.err = fmt.Errorf("prepare template database: %w", err)
				return
			}
		}
		if err := db.Close(); err != nil {
			_, _ = root.Exec(`DROP DATABASE ` + dbName)
			t.err = fmt.Errorf("close template database: %w", err)
			return
		}
		t.dbName = dbName
	})
}

func (p *Provider) nextDatabaseName(prefix string) string {
	base := sanitizeIdentifier(prefix)
	if base == "" {
		base = "urgentry_test"
	}
	return fmt.Sprintf("%s_%d_%06d", base, os.Getpid(), p.nextID.Add(1))
}

func (p *Provider) testCluster(tb testing.TB) cluster {
	tb.Helper()

	p.once.Do(func() {
		p.cluster = startCluster(p.name)
	})
	if p.cluster.err != nil {
		tb.Skipf("postgres test cluster unavailable: %v", p.cluster.err)
	}
	return p.cluster
}

func startCluster(name string) cluster {
	if os.Getenv("URGENTRY_SKIP_POSTGRES_TESTS") != "" {
		return cluster{err: fmt.Errorf("postgres tests disabled via URGENTRY_SKIP_POSTGRES_TESTS")}
	}
	if cluster := startLocalCluster(name); cluster.err == nil {
		return cluster
	}
	return startDockerCluster(name)
}

func startLocalCluster(name string) cluster {
	initdb, err := exec.LookPath("initdb")
	if err != nil {
		return cluster{err: err}
	}
	pgctl, err := exec.LookPath("pg_ctl")
	if err != nil {
		return cluster{err: err}
	}
	postgres, err := exec.LookPath("postgres")
	if err != nil {
		return cluster{err: err}
	}

	tempDir, err := os.MkdirTemp("", name+"-*")
	if err != nil {
		return cluster{err: err}
	}
	dataDir := filepath.Join(tempDir, "data")
	port, err := freePort()
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return cluster{err: err}
	}

	initCmd := exec.Command(initdb, "-A", "trust", "-U", "postgres", "-D", dataDir)
	initCmd.Env = append(os.Environ(), "POSTGRES="+postgres)
	if output, err := initCmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tempDir)
		return cluster{err: fmt.Errorf("initdb: %w: %s", err, output)}
	}

	startCmd := exec.Command(pgctl, "-D", dataDir, "-o", fmt.Sprintf("-F -p %d", port), "-w", "start")
	if output, err := startCmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tempDir)
		return cluster{err: fmt.Errorf("pg_ctl start: %w: %s", err, output)}
	}

	stop := func() {
		stopCmd := exec.Command(pgctl, "-D", dataDir, "-m", "immediate", "stop")
		_, _ = stopCmd.CombinedOutput()
		_ = os.RemoveAll(tempDir)
	}
	dsn := fmt.Sprintf("postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable", port)
	if err := waitForClusterReady(dsn, 20*time.Second); err != nil {
		stop()
		return cluster{err: fmt.Errorf("local postgres did not become primary before timeout: %w", err)}
	}
	return cluster{dsn: dsn, stop: stop}
}

func startDockerCluster(name string) cluster {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return cluster{err: err}
	}
	port, err := freePort()
	if err != nil {
		return cluster{err: err}
	}
	containerName := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	runCmd := exec.Command(
		docker,
		"run",
		"--rm",
		"-d",
		"--name", containerName,
		"-e", "POSTGRES_HOST_AUTH_METHOD=trust",
		"-e", "POSTGRES_DB=postgres",
		"-p", fmt.Sprintf("%d:5432", port),
		"postgres:17",
	)
	output, err := runCmd.CombinedOutput()
	if err != nil {
		return cluster{err: fmt.Errorf("docker postgres: %w: %s", err, output)}
	}
	stop := func() {
		stopCmd := exec.Command(docker, "rm", "-f", containerName)
		_, _ = stopCmd.CombinedOutput()
	}
	dsn := fmt.Sprintf("postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable", port)
	if err := waitForClusterReady(dsn, 20*time.Second); err != nil {
		stop()
		return cluster{err: fmt.Errorf("docker postgres did not become primary before timeout: %w", err)}
	}
	return cluster{dsn: dsn, stop: stop}
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func replaceDatabaseName(dsn, dbName string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + dbName
	return parsed.String(), nil
}

func sanitizeIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return ""
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "g_" + out
	}
	return out
}

func waitForClusterReady(dsn string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		db, err := sql.Open("pgx", dsn)
		if err == nil {
			if err := pingPrimary(db); err == nil {
				_ = db.Close()
				return nil
			} else {
				lastErr = err
			}
			_ = db.Close()
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout waiting for postgres readiness")
	}
	return lastErr
}

func pingPrimary(db *sql.DB) error {
	if err := db.Ping(); err != nil {
		return err
	}
	var primaryReady bool
	if err := db.QueryRow(`SELECT NOT pg_is_in_recovery()`).Scan(&primaryReady); err != nil {
		return err
	}
	if !primaryReady {
		return fmt.Errorf("postgres cluster is still in recovery mode")
	}
	return nil
}
