package watch

import (
	"context"
	"testing"
	"time"
)

func TestWatch_SuccessOnFirstPing(t *testing.T) {
	// Mock ping that always succeeds
	origPing := pingFunc
	pingFunc = func(ctx context.Context, ip string) bool { return true }
	defer func() { pingFunc = origPing }()

	ctx := context.Background()
	var ticks int
	err := Watch(ctx, "100.64.1.5", 100*time.Millisecond, 1*time.Second, func(elapsed time.Duration) {
		ticks++
	})
	if err != nil {
		t.Errorf("Watch() returned error: %v", err)
	}
	if ticks != 0 {
		t.Errorf("expected 0 ticks before success, got %d", ticks)
	}
}

func TestWatch_SuccessAfterRetries(t *testing.T) {
	// Mock ping that succeeds on 3rd attempt
	origPing := pingFunc
	attempts := 0
	pingFunc = func(ctx context.Context, ip string) bool {
		attempts++
		return attempts >= 3
	}
	defer func() { pingFunc = origPing }()

	ctx := context.Background()
	var ticks int
	err := Watch(ctx, "100.64.1.5", 50*time.Millisecond, 5*time.Second, func(elapsed time.Duration) {
		ticks++
	})
	if err != nil {
		t.Errorf("Watch() returned error: %v", err)
	}
	if ticks != 2 {
		t.Errorf("expected 2 ticks before success, got %d", ticks)
	}
}

func TestWatch_Timeout(t *testing.T) {
	// Mock ping that always fails
	origPing := pingFunc
	pingFunc = func(ctx context.Context, ip string) bool { return false }
	defer func() { pingFunc = origPing }()

	ctx := context.Background()
	err := Watch(ctx, "100.64.1.5", 50*time.Millisecond, 200*time.Millisecond, func(elapsed time.Duration) {})
	if err == nil {
		t.Error("Watch() should return error on timeout")
	}
}

func TestWatch_ContextCancelled(t *testing.T) {
	origPing := pingFunc
	pingFunc = func(ctx context.Context, ip string) bool { return false }
	defer func() { pingFunc = origPing }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := Watch(ctx, "100.64.1.5", 50*time.Millisecond, 5*time.Second, func(elapsed time.Duration) {})
	if err == nil {
		t.Error("Watch() should return error when context cancelled")
	}
}
