package runtimeasync

import (
	"context"
	"testing"
	"time"
)

func TestValkeyLeaseStoreHandsOffAfterExpiry(t *testing.T) {
	t.Parallel()

	redis := startMiniRedis(t)
	store, err := NewValkeyLeaseStore(redisTestURL(t, redis.Addr()))
	if err != nil {
		t.Fatalf("NewValkeyLeaseStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close store: %v", err)
		}
	})

	ctx := context.Background()
	acquired, err := store.AcquireLease(ctx, "scheduler", "worker-a", time.Second)
	if err != nil {
		t.Fatalf("AcquireLease first: %v", err)
	}
	if !acquired {
		t.Fatal("expected first holder to acquire lease")
	}

	acquired, err = store.AcquireLease(ctx, "scheduler", "worker-b", time.Second)
	if err != nil {
		t.Fatalf("AcquireLease second holder: %v", err)
	}
	if acquired {
		t.Fatal("expected second holder to be fenced before expiry")
	}

	redis.FastForward(2 * time.Second)

	acquired, err = store.AcquireLease(ctx, "scheduler", "worker-b", time.Second)
	if err != nil {
		t.Fatalf("AcquireLease after expiry: %v", err)
	}
	if !acquired {
		t.Fatal("expected second holder to acquire lease after expiry")
	}
}
