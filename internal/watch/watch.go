package watch

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// pingFunc is the function used to ping an IP. Replaceable for testing.
var pingFunc = doPing

// Watch pings the given IP every interval until it responds or timeout elapses.
// Calls onTick with the elapsed duration on each failed ping attempt.
// Returns nil on success, error on timeout or context cancellation.
func Watch(ctx context.Context, ip string, interval, timeout time.Duration, onTick func(elapsed time.Duration)) error {
	deadline := time.After(timeout)
	start := time.Now()

	// Try immediately first
	if pingFunc(ctx, ip) {
		return nil
	}
	onTick(time.Since(start))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timeout after %s: device did not respond", timeout)
		case <-ticker.C:
			if pingFunc(ctx, ip) {
				return nil
			}
			onTick(time.Since(start))
		}
	}
}

// doPing sends a single ICMP ping and returns true if the host responds.
func doPing(ctx context.Context, ip string) bool {
	cmd := exec.CommandContext(ctx, "/sbin/ping", "-c", "1", "-W", "1", ip)
	return cmd.Run() == nil
}
