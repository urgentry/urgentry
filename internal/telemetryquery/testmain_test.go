package telemetryquery

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if benchmarkBridgeFixtureCleanup != nil {
		benchmarkBridgeFixtureCleanup()
	}
	bridgeQueryPostgres.Close()
	os.Exit(code)
}
