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
	cfg         *config.Config
	client      *cloud.Client
	session     *power.Session
	sessionDone chan *power.Session
	lastCheck   time.Time
	onACPower   bool
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
	log.Printf("wakeup daemon starting (device=%s, ac_interval=%s, battery_interval=%s)",
		d.cfg.DeviceID, d.cfg.ACCheckInterval, d.cfg.BatteryCheckInterval)

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

	// Run first check immediately
	d.check(ctx)

	for {
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

	sig, err := d.client.Check(ctx, d.cfg.DeviceID)
	if err != nil {
		if ctx.Err() != nil {
			return // shutting down, don't log
		}
		log.Printf("check failed (will retry next cycle): %v", err)
		d.lastCheck = time.Now()
		return
	}

	d.lastCheck = time.Now()

	if sig == nil {
		log.Printf("no wake signal, scheduling next wake")
		d.scheduleNextWake()
		return
	}

	duration := time.Duration(sig.Duration) * time.Second
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
