package config

import (
	"testing"
	"time"
)

func TestMergeRemote_OverridesFields(t *testing.T) {
	base := &Config{
		WorkerURL:            "https://example.com",
		Token:                "tok",
		DeviceID:             "dev1",
		CheckInterval:        15 * time.Minute,
		DefaultDuration:      30 * time.Minute,
		ACCheckInterval:      2 * time.Minute,
		BatteryCheckInterval: 15 * time.Minute,
		WakeDetectInterval:   30 * time.Second,
	}

	remote := &RemoteConfig{
		CheckInterval:   120,  // 2 minutes
		DefaultDuration: 3600, // 1 hour
	}

	merged, err := MergeRemote(base, remote)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.CheckInterval != 2*time.Minute {
		t.Errorf("expected check_interval 2m, got %s", merged.CheckInterval)
	}
	if merged.DefaultDuration != 1*time.Hour {
		t.Errorf("expected default_duration 1h, got %s", merged.DefaultDuration)
	}
	// Unchanged fields should remain
	if merged.ACCheckInterval != 2*time.Minute {
		t.Errorf("expected ac_check_interval 2m, got %s", merged.ACCheckInterval)
	}
	// Protected fields should remain
	if merged.WorkerURL != "https://example.com" {
		t.Errorf("worker_url should not change, got %s", merged.WorkerURL)
	}
	if merged.Token != "tok" {
		t.Errorf("token should not change, got %s", merged.Token)
	}
	if merged.DeviceID != "dev1" {
		t.Errorf("device_id should not change, got %s", merged.DeviceID)
	}
}

func TestMergeRemote_NilRemote(t *testing.T) {
	base := &Config{
		WorkerURL:            "https://example.com",
		Token:                "tok",
		CheckInterval:        15 * time.Minute,
		DefaultDuration:      30 * time.Minute,
		ACCheckInterval:      2 * time.Minute,
		BatteryCheckInterval: 15 * time.Minute,
		WakeDetectInterval:   30 * time.Second,
	}

	merged, err := MergeRemote(base, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged != base {
		t.Error("nil remote should return base config unchanged")
	}
}

func TestMergeRemote_ZeroValuesNoOverride(t *testing.T) {
	base := &Config{
		WorkerURL:            "https://example.com",
		Token:                "tok",
		CheckInterval:        15 * time.Minute,
		DefaultDuration:      30 * time.Minute,
		ACCheckInterval:      2 * time.Minute,
		BatteryCheckInterval: 15 * time.Minute,
		WakeDetectInterval:   30 * time.Second,
	}

	// All zero values — should not override anything
	remote := &RemoteConfig{}

	merged, err := MergeRemote(base, remote)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.CheckInterval != 15*time.Minute {
		t.Errorf("zero remote should not override check_interval, got %s", merged.CheckInterval)
	}
}

func TestMergeRemote_ValidationFailure(t *testing.T) {
	base := &Config{
		WorkerURL:            "https://example.com",
		Token:                "tok",
		CheckInterval:        15 * time.Minute,
		DefaultDuration:      30 * time.Minute,
		ACCheckInterval:      2 * time.Minute,
		BatteryCheckInterval: 15 * time.Minute,
		WakeDetectInterval:   30 * time.Second,
	}

	// Invalid: check_interval < 1m
	remote := &RemoteConfig{
		CheckInterval: 10, // 10 seconds — too short
	}

	merged, err := MergeRemote(base, remote)
	if err == nil {
		t.Fatal("expected validation error")
	}
	// Should return base config on validation failure
	if merged.CheckInterval != 15*time.Minute {
		t.Errorf("should return base config on error, got %s", merged.CheckInterval)
	}
}

func TestMergeRemote_DarkwakeDetection(t *testing.T) {
	base := &Config{
		WorkerURL:               "https://example.com",
		Token:                   "tok",
		CheckInterval:           15 * time.Minute,
		DefaultDuration:         30 * time.Minute,
		ACCheckInterval:         2 * time.Minute,
		BatteryCheckInterval:    15 * time.Minute,
		EnableDarkwakeDetection: false,
		WakeDetectInterval:      30 * time.Second,
	}

	enabled := true
	remote := &RemoteConfig{
		EnableDarkwakeDetection: &enabled,
	}

	merged, err := MergeRemote(base, remote)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !merged.EnableDarkwakeDetection {
		t.Error("expected darkwake detection to be enabled")
	}
}

func TestToRemoteConfig(t *testing.T) {
	cfg := &Config{
		WorkerURL:               "https://example.com",
		Token:                   "tok",
		DeviceID:                "dev1",
		CheckInterval:           2 * time.Minute,
		DefaultDuration:         30 * time.Minute,
		ACCheckInterval:         2 * time.Minute,
		BatteryCheckInterval:    15 * time.Minute,
		EnableDarkwakeDetection: true,
		WakeDetectInterval:      30 * time.Second,
	}

	remote := ToRemoteConfig(cfg)
	if remote.CheckInterval != 120 {
		t.Errorf("expected check_interval 120, got %d", remote.CheckInterval)
	}
	if remote.DefaultDuration != 1800 {
		t.Errorf("expected default_duration 1800, got %d", remote.DefaultDuration)
	}
	if remote.EnableDarkwakeDetection == nil || !*remote.EnableDarkwakeDetection {
		t.Error("expected darkwake detection enabled")
	}
}
