package daemon

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/d0zingcat/wakeup-macos/internal/cloud"
	"github.com/d0zingcat/wakeup-macos/internal/config"
	"github.com/d0zingcat/wakeup-macos/internal/power"
)

type Daemon struct {
	cfg           *config.Config
	client        *cloud.Client
	session       *power.Session
	sessionDone   chan *power.Session
	lastCheck     time.Time
	onACPower     bool
	configVersion string // remote config version hash
	// darkwake detection (Phase 2) — wall clock only, no monotonic
	lastWallTime time.Time
}

func New(cfg *config.Config) *Daemon {
	return &Daemon{
		cfg:         cfg,
		client:      cloud.NewClient(cfg.WorkerURL, cfg.Token),
		sessionDone: make(chan *power.Session, 1),
		onACPower:   true, // assume AC until first check
	}
}

func (d *Daemon) Run() error {
	log.Printf("wakeup daemon starting (device=%s, ac_interval=%s, battery_interval=%s, darkwake=%v)",
		d.cfg.DeviceID, d.cfg.ACCheckInterval, d.cfg.BatteryCheckInterval, d.cfg.EnableDarkwakeDetection)

	// Clean up any orphaned caffeinate processes from previous runs
	power.CleanOrphanCaffeinate()

	// Detect initial power state and schedule first wake
	d.onACPower = power.IsOnACPower()
	d.scheduleNextWake()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Use the shorter AC interval as the ticker base.
	// On battery, we skip checks that are too soon (throttle via lastCheck).
	ticker := time.NewTicker(d.cfg.ACCheckInterval)
	defer ticker.Stop()

	// Track current intervals for change detection
	currentACInterval := d.cfg.ACCheckInterval
	currentDarkwake := d.cfg.EnableDarkwakeDetection
	currentWakeDetect := d.cfg.WakeDetectInterval

	// Darkwake detection: fast ticker using wall clock jump detection
	var wakeTicker *time.Ticker
	var wakeTickerC <-chan time.Time
	if d.cfg.EnableDarkwakeDetection {
		log.Printf("darkwake detection enabled (interval=%s)", d.cfg.WakeDetectInterval)
		wakeTicker = time.NewTicker(d.cfg.WakeDetectInterval)
		wakeTickerC = wakeTicker.C
		d.lastWallTime = wallNow()
		defer func() {
			if wakeTicker != nil {
				wakeTicker.Stop()
			}
		}()
	}

	// Run first check immediately
	d.check(ctx)

	for {
		// Hot-reload: rebuild tickers if config changed
		if d.cfg.ACCheckInterval != currentACInterval {
			log.Printf("hot-reload: ac_check_interval changed %s -> %s", currentACInterval, d.cfg.ACCheckInterval)
			ticker.Reset(d.cfg.ACCheckInterval)
			currentACInterval = d.cfg.ACCheckInterval
		}
		if d.cfg.EnableDarkwakeDetection != currentDarkwake || d.cfg.WakeDetectInterval != currentWakeDetect {
			if d.cfg.EnableDarkwakeDetection {
				if wakeTicker == nil {
					log.Printf("hot-reload: darkwake detection enabled (interval=%s)", d.cfg.WakeDetectInterval)
					wakeTicker = time.NewTicker(d.cfg.WakeDetectInterval)
					d.lastWallTime = wallNow()
				} else if d.cfg.WakeDetectInterval != currentWakeDetect {
					log.Printf("hot-reload: wake_detect_interval changed %s -> %s", currentWakeDetect, d.cfg.WakeDetectInterval)
					wakeTicker.Reset(d.cfg.WakeDetectInterval)
				}
				wakeTickerC = wakeTicker.C
			} else if wakeTicker != nil {
				log.Printf("hot-reload: darkwake detection disabled")
				wakeTicker.Stop()
				wakeTicker = nil
				wakeTickerC = nil
			}
			currentDarkwake = d.cfg.EnableDarkwakeDetection
			currentWakeDetect = d.cfg.WakeDetectInterval
		}

		select {
		case <-ticker.C:
			d.onACPower = power.IsOnACPower()
			interval := d.currentInterval()
			d.scheduleNextWake()

			// On battery, skip if we checked too recently
			if !d.onACPower && time.Since(d.lastCheck) < interval {
				continue
			}
			d.check(ctx)

		case <-wakeTickerC:
			now := wallNow()
			elapsed := now.Sub(d.lastWallTime)
			d.lastWallTime = now

			// Time jump detected — system just woke from sleep
			if elapsed > d.cfg.WakeDetectInterval*2 {
				log.Printf("darkwake detected (wall clock jumped %s)", elapsed.Truncate(time.Second))
				// Only check if enough time passed since last check
				if time.Since(d.lastCheck) > d.cfg.WakeDetectInterval {
					d.check(ctx)
				}
			}

		case sess := <-d.sessionDone:
			// Only act if this is still the current session
			if sess == d.session {
				log.Printf("caffeinate session ended, scheduling next wake")
				d.session = nil
				d.scheduleNextWake()
			}

		case sig := <-sigCh:
			log.Printf("received signal %v, shutting down", sig)
			cancel()
			d.shutdown()
			return nil
		}
	}
}

func (d *Daemon) check(ctx context.Context) {
	log.Printf("checking for wake signal (device=%s, power=%s)",
		d.cfg.DeviceID, d.powerStateStr())

	result, err := d.client.Check(ctx, d.cfg.DeviceID, d.configVersion)
	if err != nil {
		if ctx.Err() != nil {
			return // shutting down, don't log
		}
		log.Printf("check failed (will retry next cycle): %v", err)
		d.lastCheck = time.Now()
		return
	}

	d.lastCheck = time.Now()

	// Apply remote config if changed
	if result.Config != nil && result.ConfigVersion != d.configVersion {
		d.applyRemoteConfig(result)
	}

	if result.Signal == nil {
		log.Printf("no wake signal, scheduling next wake")
		d.scheduleNextWake()
		return
	}

	duration := time.Duration(result.Signal.Duration) * time.Second
	if duration < 1*time.Minute {
		duration = d.cfg.DefaultDuration
	}

	log.Printf("wake signal received! keeping awake for %s", duration)

	// If already awake, stop the old session and start a new one
	if d.session != nil {
		log.Printf("extending wake: stopping previous caffeinate session")
		d.session.Stop()
		d.session = nil
	}

	session, err := power.KeepAwake(duration)
	if err != nil {
		log.Printf("failed to start caffeinate: %v", err)
		d.scheduleNextWake()
		return
	}
	d.session = session

	// Monitor the session in background — notify via channel, not direct mutation
	go func() {
		<-session.Done()
		d.sessionDone <- session
	}()
}

// applyRemoteConfig merges remote config into the running config and
// returns whether the config actually changed (requiring ticker rebuild).
func (d *Daemon) applyRemoteConfig(result *cloud.CheckResult) {
	// Convert cloud.RemoteConfig to config.RemoteConfig
	remoteCfg := &config.RemoteConfig{
		CheckInterval:           result.Config.CheckInterval,
		DefaultDuration:         result.Config.DefaultDuration,
		ACCheckInterval:         result.Config.ACCheckInterval,
		BatteryCheckInterval:    result.Config.BatteryCheckInterval,
		EnableDarkwakeDetection: result.Config.EnableDarkwakeDetection,
		WakeDetectInterval:      result.Config.WakeDetectInterval,
	}

	merged, err := config.MergeRemote(d.cfg, remoteCfg)
	if err != nil {
		log.Printf("remote config validation failed, keeping current config: %v", err)
		return
	}

	d.cfg = merged
	d.configVersion = result.ConfigVersion
	log.Printf("config updated from remote (version: %s)", result.ConfigVersion)
}

func (d *Daemon) currentInterval() time.Duration {
	if d.onACPower {
		return d.cfg.ACCheckInterval
	}
	return d.cfg.BatteryCheckInterval
}

func (d *Daemon) scheduleNextWake() {
	interval := d.currentInterval()
	if err := power.ScheduleNextWake(interval); err != nil {
		log.Printf("failed to schedule next wake: %v", err)
		log.Printf("the system may not wake automatically — will retry on next check")
	}
}

func (d *Daemon) shutdown() {
	if d.session != nil {
		log.Printf("stopping caffeinate session")
		d.session.Stop()
		d.session = nil
	}
	log.Printf("daemon stopped")
}

func (d *Daemon) powerStateStr() string {
	if d.onACPower {
		return "AC"
	}
	return "battery"
}

// wallNow returns the current time with the monotonic clock reading stripped.
// This is critical for detecting sleep/wake: Go's monotonic clock stops during
// macOS sleep, so time.Since() won't show the jump. Wall clock does.
// See: https://github.com/golang/go/issues/36141
func wallNow() time.Time {
	return time.Now().Round(0)
}
