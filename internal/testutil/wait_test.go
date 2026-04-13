package testutil

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWait_ImmediateSuccess(t *testing.T) {
	err := Wait(time.Second, 10*time.Millisecond, func() (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWait_SucceedsAfterRetries(t *testing.T) {
	var calls atomic.Int32
	err := Wait(time.Second, 5*time.Millisecond, func() (bool, error) {
		if calls.Add(1) >= 3 {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if c := calls.Load(); c < 3 {
		t.Fatalf("expected at least 3 calls, got %d", c)
	}
}

func TestWait_Timeout(t *testing.T) {
	err := Wait(50*time.Millisecond, 5*time.Millisecond, func() (bool, error) {
		return false, nil
	})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestWait_TimeoutReturnsLastError(t *testing.T) {
	sentinel := errors.New("custom error")
	err := Wait(50*time.Millisecond, 5*time.Millisecond, func() (bool, error) {
		return false, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

func TestWait_DefaultTimeoutAndInterval(t *testing.T) {
	// Passing zero triggers defaults (1s timeout, 10ms interval).
	// We return true immediately so it won't actually wait.
	err := Wait(0, 0, func() (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("expected nil with zero args, got %v", err)
	}
}

func TestWait_ErrorThenSuccess(t *testing.T) {
	var calls atomic.Int32
	sentinel := errors.New("temporary")
	err := Wait(time.Second, 5*time.Millisecond, func() (bool, error) {
		if calls.Add(1) < 3 {
			return false, sentinel
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("expected nil after recovery, got %v", err)
	}
}
