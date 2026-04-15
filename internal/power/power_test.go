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

func TestParseACPower(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantAC bool
	}{
		{
			name:   "AC power",
			input:  "Now drawing from 'AC Power'\n -InternalBattery-0 (id=1234)\t100%; charged; 0:00 remaining present: true",
			wantAC: true,
		},
		{
			name:   "Battery power",
			input:  "Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1234)\t85%; discharging; 3:45 remaining present: true",
			wantAC: false,
		},
		{
			name:   "empty string",
			input:  "",
			wantAC: true,
		},
		{
			name:   "unexpected format",
			input:  "Something unexpected from UPS",
			wantAC: true,
		},
		{
			name:   "AC power with extra whitespace",
			input:  "Now drawing from 'AC Power'\n",
			wantAC: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseACPower(tt.input)
			if got != tt.wantAC {
				t.Errorf("parseACPower(%q) = %v, want %v", tt.input, got, tt.wantAC)
			}
		})
	}
}
