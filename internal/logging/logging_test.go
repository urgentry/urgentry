package logging

import (
	"testing"

	"github.com/rs/zerolog"
)

func TestSetup_Development(t *testing.T) {
	Setup("development")

	if zerolog.GlobalLevel() != zerolog.DebugLevel {
		t.Errorf("expected debug level for development, got %v", zerolog.GlobalLevel())
	}
}

func TestSetup_Production(t *testing.T) {
	Setup("production")

	if zerolog.GlobalLevel() != zerolog.InfoLevel {
		t.Errorf("expected info level for production, got %v", zerolog.GlobalLevel())
	}
}

func TestSetup_EmptyString(t *testing.T) {
	Setup("")

	if zerolog.GlobalLevel() != zerolog.InfoLevel {
		t.Errorf("expected info level for empty env, got %v", zerolog.GlobalLevel())
	}
}

func TestSetup_ArbitraryString(t *testing.T) {
	Setup("staging")

	if zerolog.GlobalLevel() != zerolog.InfoLevel {
		t.Errorf("expected info level for non-development env, got %v", zerolog.GlobalLevel())
	}
}
