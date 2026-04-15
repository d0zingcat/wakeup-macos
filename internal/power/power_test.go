package power

import (
	"testing"
	"time"
)

func TestKeepAwake_StartsAndStops(t *testing.T) {
	session, err := KeepAwake(5 * time.Second)
	if err != nil {
		t.Fatalf("KeepAwake failed: %v", err)
	}

	// Should be running
	select {
	case <-session.Done():
		t.Fatal("session exited too early")
	default:
	}

	// Stop early
	session.Stop()

	// Should be done now
	select {
	case <-session.Done():
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("session did not stop in time")
	}
}

func TestKeepAwake_DoubleStop(t *testing.T) {
	session, err := KeepAwake(5 * time.Second)
	if err != nil {
		t.Fatalf("KeepAwake failed: %v", err)
	}

	session.Stop()
	// Second stop should not panic
	session.Stop()
}

func TestKeepAwake_NaturalExpiry(t *testing.T) {
	session, err := KeepAwake(1 * time.Second)
	if err != nil {
		t.Fatalf("KeepAwake failed: %v", err)
	}

	select {
	case <-session.Done():
		// ok - exited naturally
	case <-time.After(5 * time.Second):
		t.Fatal("session did not expire naturally")
	}
}
