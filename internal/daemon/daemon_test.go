package daemon

import (
	"testing"
	"time"
)

func TestWallNow_StripsMonotonic(t *testing.T) {
	now := wallNow()
	// Round(0) strips the monotonic reading.
	// Verify by checking that the time has no monotonic component:
	// time.Time.String() includes "m=+..." when monotonic is present.
	s := now.String()
	for _, c := range []string{"m=+", "m=-"} {
		if contains(s, c) {
			t.Errorf("wallNow() should strip monotonic reading, got: %s", s)
		}
	}
}

func TestWallNow_TimeJumpDetection(t *testing.T) {
	// Simulate: lastWallTime was 2 minutes ago (as if system slept)
	lastWallTime := wallNow().Add(-2 * time.Minute)
	now := wallNow()
	elapsed := now.Sub(lastWallTime)

	wakeDetectInterval := 30 * time.Second

	// Should detect a jump: elapsed (2m) > 2 * interval (1m)
	if elapsed <= wakeDetectInterval*2 {
		t.Errorf("expected time jump detection, elapsed=%s, threshold=%s", elapsed, wakeDetectInterval*2)
	}
}

func TestWallNow_NoFalsePositive(t *testing.T) {
	// Two calls in quick succession should NOT trigger jump detection
	lastWallTime := wallNow()
	time.Sleep(10 * time.Millisecond)
	now := wallNow()
	elapsed := now.Sub(lastWallTime)

	wakeDetectInterval := 30 * time.Second

	// Should NOT detect a jump
	if elapsed > wakeDetectInterval*2 {
		t.Errorf("false positive: elapsed=%s, threshold=%s", elapsed, wakeDetectInterval*2)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
