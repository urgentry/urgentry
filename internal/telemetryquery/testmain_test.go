package telemetryquery

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	bridgeQueryPostgres.Close()
	os.Exit(code)
}
