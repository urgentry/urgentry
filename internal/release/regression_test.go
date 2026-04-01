package release

import (
	"context"
	"testing"

	"urgentry/internal/alert"
)

type mockHealthProvider struct {
	rates map[string]ReleaseHealth
}

func (m *mockHealthProvider) GetCrashFreeRate(_ context.Context, _, version string) (ReleaseHealth, error) {
	if h, ok := m.rates[version]; ok {
		return h, nil
	}
	return ReleaseHealth{Version: version, CrashFreeRate: 100}, nil
}

type mockAlertSink struct {
	signals []alert.Signal
}

func (m *mockAlertSink) FireSignal(_ context.Context, sig alert.Signal) error {
	m.signals = append(m.signals, sig)
	return nil
}

func TestRegressionDetector_NoRegression(t *testing.T) {
	health := &mockHealthProvider{rates: map[string]ReleaseHealth{
		"1.0.0": {Version: "1.0.0", CrashFreeRate: 98.0, SessionCount: 1000},
		"1.1.0": {Version: "1.1.0", CrashFreeRate: 97.5, SessionCount: 500},
	}}
	sink := &mockAlertSink{}
	d := NewRegressionDetector(health, sink)

	result, err := d.Compare(context.Background(), "p1", "1.1.0", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if result.IsRegression {
		t.Error("expected no regression for 0.5% drop")
	}
	if result.AlertFired {
		t.Error("should not fire alert without regression")
	}
	if len(sink.signals) != 0 {
		t.Errorf("expected 0 signals, got %d", len(sink.signals))
	}
}

func TestRegressionDetector_Regression(t *testing.T) {
	health := &mockHealthProvider{rates: map[string]ReleaseHealth{
		"1.0.0": {Version: "1.0.0", CrashFreeRate: 98.0, SessionCount: 1000},
		"1.1.0": {Version: "1.1.0", CrashFreeRate: 85.0, SessionCount: 500, AffectedUsers: 50},
	}}
	sink := &mockAlertSink{}
	d := NewRegressionDetector(health, sink)

	result, err := d.Compare(context.Background(), "p1", "1.1.0", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsRegression {
		t.Error("expected regression for 13% drop")
	}
	if !result.AlertFired {
		t.Error("expected alert to fire")
	}
	if len(sink.signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(sink.signals))
	}
	sig := sink.signals[0]
	if sig.Release != "1.1.0" {
		t.Errorf("signal release=%v, want 1.1.0", sig.Release)
	}
	if !sig.IsRegression {
		t.Error("signal should have IsRegression=true")
	}
}

func TestRegressionDetector_CustomThreshold(t *testing.T) {
	health := &mockHealthProvider{rates: map[string]ReleaseHealth{
		"1.0.0": {Version: "1.0.0", CrashFreeRate: 98.0},
		"1.1.0": {Version: "1.1.0", CrashFreeRate: 92.0},
	}}
	sink := &mockAlertSink{}
	d := NewRegressionDetector(health, sink).WithThreshold(5.0)

	result, err := d.Compare(context.Background(), "p1", "1.1.0", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsRegression {
		t.Error("expected regression with 5% threshold and 6% drop")
	}
	if result.ThresholdUsed != 5.0 {
		t.Errorf("threshold=%v, want 5.0", result.ThresholdUsed)
	}
}

func TestRegressionDetector_NilSink(t *testing.T) {
	health := &mockHealthProvider{rates: map[string]ReleaseHealth{
		"1.0.0": {Version: "1.0.0", CrashFreeRate: 98.0},
		"1.1.0": {Version: "1.1.0", CrashFreeRate: 80.0},
	}}
	d := NewRegressionDetector(health, nil)

	result, err := d.Compare(context.Background(), "p1", "1.1.0", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsRegression {
		t.Error("expected regression")
	}
	if result.AlertFired {
		t.Error("should not fire with nil sink")
	}
}
