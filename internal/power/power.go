package power

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Session represents an active caffeinate session that prevents sleep.
type Session struct {
	cmd  *exec.Cmd
	done chan struct{}
	mu   sync.Mutex
}

// KeepAwake starts a caffeinate process to prevent system sleep for the given duration.
// Returns a Session that can be used to stop early.
func KeepAwake(duration time.Duration) (*Session, error) {
	secs := int(duration.Seconds())
	if secs < 1 {
		secs = 1
	}

	cmd := exec.Command("caffeinate", "-s", "-t", strconv.Itoa(secs))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start caffeinate: %w", err)
	}

	s := &Session{
		cmd:  cmd,
		done: make(chan struct{}),
	}

	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("caffeinate exited: %v", err)
		}
		close(s.done)
	}()

	return s, nil
}

// Stop terminates the caffeinate session early.
func (s *Session) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd.Process == nil {
		return
	}

	select {
	case <-s.done:
		// Already exited
		return
	default:
	}

	if err := s.cmd.Process.Kill(); err != nil {
		log.Printf("kill caffeinate: %v", err)
	}
	<-s.done
}

// Done returns a channel that is closed when the caffeinate process exits.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// ScheduleNextWake schedules the next hardware wake using pmset relative wake.
// This is the core scheduling mechanism — called after each check cycle.
func ScheduleNextWake(after time.Duration) error {
	secs := int(after.Seconds())
	if secs < 60 {
		secs = 60
	}

	cmd := exec.Command("sudo", "pmset", "relative", "wake", strconv.Itoa(secs))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pmset relative wake: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SetupRepeatWake configures pmset repeat as a fallback wake schedule.
// This ensures the Mac wakes at least once per day even if the chain breaks.
func SetupRepeatWake() error {
	// Wake every day at 08:00 as a safety net
	cmd := exec.Command("sudo", "pmset", "repeat", "wakeorpoweron", "MTWRFSU", "08:00:00")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pmset repeat: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ClearRepeatWake removes the pmset repeat configuration.
func ClearRepeatWake() error {
	cmd := exec.Command("sudo", "pmset", "repeat", "cancel")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pmset repeat cancel: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// IsOnACPower returns true if the Mac is currently on AC power.
// Returns true on error (conservative default: use shorter interval).
func IsOnACPower() bool {
	out, err := exec.Command("pmset", "-g", "ps").Output()
	if err != nil {
		return true
	}
	return parseACPower(string(out))
}

// parseACPower parses the output of `pmset -g ps` and returns true if on AC power.
// Returns true for empty/unexpected input (conservative default).
func parseACPower(output string) bool {
	if output == "" {
		return true
	}
	firstLine := strings.SplitN(output, "\n", 2)[0]
	if strings.Contains(firstLine, "Battery Power") {
		return false
	}
	return true
}

// CleanOrphanCaffeinate kills any orphaned caffeinate processes from previous runs.
func CleanOrphanCaffeinate() {
	out, err := exec.Command("pgrep", "-f", "caffeinate -s -t").Output()
	if err != nil {
		return // no orphans
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		// Don't kill our own process
		if pid == os.Getpid() {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		log.Printf("killing orphan caffeinate process %d", pid)
		proc.Kill()
	}
}
