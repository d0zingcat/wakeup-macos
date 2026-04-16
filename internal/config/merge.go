package config

import "time"

// RemoteConfig contains fields that can be managed remotely via the Worker.
// Zero values mean "not set" and will not override local config.
type RemoteConfig struct {
	CheckInterval           int   `json:"check_interval,omitempty"`            // seconds
	DefaultDuration         int   `json:"default_duration,omitempty"`          // seconds
	ACCheckInterval         int   `json:"ac_check_interval,omitempty"`         // seconds
	BatteryCheckInterval    int   `json:"battery_check_interval,omitempty"`    // seconds
	EnableDarkwakeDetection *bool `json:"enable_darkwake_detection,omitempty"` // pointer to distinguish false from unset
	WakeDetectInterval      int   `json:"wake_detect_interval,omitempty"`      // seconds
}

// MergeRemote applies remote config values on top of a base config.
// Only non-zero remote fields override the base. Returns a new Config
// and an error if the merged result fails validation.
// The fields worker_url, token, and device_id are never affected.
func MergeRemote(base *Config, remote *RemoteConfig) (*Config, error) {
	if remote == nil {
		return base, nil
	}

	// Copy base config
	merged := *base

	if remote.CheckInterval > 0 {
		merged.CheckInterval = time.Duration(remote.CheckInterval) * time.Second
	}
	if remote.DefaultDuration > 0 {
		merged.DefaultDuration = time.Duration(remote.DefaultDuration) * time.Second
	}
	if remote.ACCheckInterval > 0 {
		merged.ACCheckInterval = time.Duration(remote.ACCheckInterval) * time.Second
	}
	if remote.BatteryCheckInterval > 0 {
		merged.BatteryCheckInterval = time.Duration(remote.BatteryCheckInterval) * time.Second
	}
	if remote.EnableDarkwakeDetection != nil {
		merged.EnableDarkwakeDetection = *remote.EnableDarkwakeDetection
	}
	if remote.WakeDetectInterval > 0 {
		merged.WakeDetectInterval = time.Duration(remote.WakeDetectInterval) * time.Second
	}

	if err := merged.Validate(); err != nil {
		return base, err
	}

	return &merged, nil
}

// ToRemoteConfig converts the remotely-manageable fields of a Config
// to a RemoteConfig struct suitable for pushing to the Worker.
func ToRemoteConfig(cfg *Config) *RemoteConfig {
	darkwake := cfg.EnableDarkwakeDetection
	return &RemoteConfig{
		CheckInterval:           int(cfg.CheckInterval.Seconds()),
		DefaultDuration:         int(cfg.DefaultDuration.Seconds()),
		ACCheckInterval:         int(cfg.ACCheckInterval.Seconds()),
		BatteryCheckInterval:    int(cfg.BatteryCheckInterval.Seconds()),
		EnableDarkwakeDetection: &darkwake,
		WakeDetectInterval:      int(cfg.WakeDetectInterval.Seconds()),
	}
}
