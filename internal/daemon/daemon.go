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
	cfg     *config.Config
	client  *cloud.Client
	session *power.Session
}

func New(cfg *config.Config) *Daemon {
	return &Daemon{
		cfg:    cfg,
		client: cloud.NewClient(cfg.WorkerURL, cfg.Token),
	}
}

func (d *Daemon) Run() error {
	log.Printf("wakeup daemon starting (device=%s, interval=%s)", d.cfg.DeviceID, d.cfg.CheckInterval)

	// Clean up any orphaned caffeinate processes from previous runs
	power.CleanOrphanCaffeinate()

	// Schedule the next wake immediately
	d.scheduleNextWake()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	ticker := time.NewTicker(d.cfg.CheckInterval)
	defer ticker.Stop()

	// Run first check immediately
	d.check()

	for {
		select {
		case <-ticker.C:
			d.check()
		case <-ctx.Done():
			d.shutdown()
			return nil
		case sig := <-sigCh:
			log.Printf("received signal %v, shutting down", sig)
			d.shutdown()
			return nil
		}
	}
}

func (d *Daemon) check() {
	log.Printf("checking for wake signal (device=%s)", d.cfg.DeviceID)

	signal, err := d.client.Check(d.cfg.DeviceID)
	if err != nil {
		log.Printf("check failed (will retry next cycle): %v", err)
		d.scheduleNextWake()
		return
	}

	if signal == nil {
		log.Printf("no wake signal, scheduling next wake")
		d.scheduleNextWake()
		return
	}

	duration := time.Duration(signal.Duration) * time.Second
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

	// Monitor the session in background
	go func() {
		<-session.Done()
		log.Printf("caffeinate session ended, scheduling next wake")
		d.session = nil
		d.scheduleNextWake()
	}()
}

func (d *Daemon) scheduleNextWake() {
	if err := power.ScheduleNextWake(d.cfg.CheckInterval); err != nil {
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
