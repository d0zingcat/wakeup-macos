package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/d0zingcat/wakeup-macos/internal/cloud"
	"github.com/d0zingcat/wakeup-macos/internal/config"
	"github.com/d0zingcat/wakeup-macos/internal/daemon"
	"github.com/d0zingcat/wakeup-macos/internal/power"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "daemon":
		runDaemon()
	case "send":
		runSend()
	case "status":
		runStatus()
	case "devices":
		runDevices()
	case "install":
		runInstall()
	case "uninstall":
		runUninstall()
	case "version":
		fmt.Println("wakeup", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`wakeup - macOS remote wake daemon via Cloudflare Workers

Usage:
  wakeup daemon                       Run the wake-check daemon (foreground)
  wakeup send <device_id> [duration]  Send wake signal to a device (default: 30m)
  wakeup send --all [duration]        Send wake signal to all devices
  wakeup status                       Show status of all devices
  wakeup devices                      List registered devices
  wakeup install                      Install as launchd daemon (requires sudo)
  wakeup uninstall                    Uninstall launchd daemon (requires sudo)
  wakeup version                      Print version

Duration examples: 30m, 1h, 2h30m`)
}

func runDaemon() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	if cfg.DeviceID == "" {
		fmt.Fprintf(os.Stderr, "device_id is required for daemon mode\n")
		os.Exit(1)
	}

	d := daemon.New(cfg)
	if err := d.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
}

func runSend() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	client := cloud.NewClient(cfg.WorkerURL, cfg.Token)
	args := os.Args[2:]

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: wakeup send <device_id> [duration]")
		fmt.Fprintln(os.Stderr, "       wakeup send --all [duration]")
		os.Exit(1)
	}

	var all bool
	var deviceID string
	var durationStr string

	if args[0] == "--all" {
		all = true
		if len(args) > 1 {
			durationStr = args[1]
		}
	} else {
		deviceID = args[0]
		if len(args) > 1 {
			durationStr = args[1]
		}
	}

	duration := cfg.DefaultDuration
	if durationStr != "" {
		d, err := parseDuration(durationStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid duration: %v\n", err)
			os.Exit(1)
		}
		duration = d
	}

	if all {
		err = client.SendAll(duration)
		if err != nil {
			fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Wake signal sent to all devices (duration: %s)\n", duration)
	} else {
		err = client.Send(deviceID, duration)
		if err != nil {
			fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Wake signal sent to %s (duration: %s)\n", deviceID, duration)
	}
}

func runStatus() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	client := cloud.NewClient(cfg.WorkerURL, cfg.Token)
	status, err := client.Status()
	if err != nil {
		fmt.Fprintf(os.Stderr, "status failed: %v\n", err)
		os.Exit(1)
	}

	if len(status) == 0 {
		fmt.Println("No devices registered")
		return
	}

	for id, s := range status {
		wake := "idle"
		if s.PendingWake {
			wake = "PENDING WAKE"
		}
		lastSeen := "never"
		if s.LastSeen > 0 {
			t := time.UnixMilli(s.LastSeen)
			lastSeen = time.Since(t).Truncate(time.Second).String() + " ago"
		}
		fmt.Printf("  %-20s  %s  (last seen: %s)\n", id, wake, lastSeen)
	}
}

func runDevices() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	client := cloud.NewClient(cfg.WorkerURL, cfg.Token)
	devices, err := client.Devices()
	if err != nil {
		fmt.Fprintf(os.Stderr, "devices failed: %v\n", err)
		os.Exit(1)
	}

	if len(devices) == 0 {
		fmt.Println("No devices registered")
		return
	}

	for id, d := range devices {
		lastSeen := "never"
		if d.LastSeen > 0 {
			t := time.UnixMilli(d.LastSeen)
			lastSeen = time.Since(t).Truncate(time.Second).String() + " ago"
		}
		fmt.Printf("  %-20s  last seen: %s\n", id, lastSeen)
	}
}

const (
	binPath    = "/usr/local/bin/wakeup"
	plistName  = "com.wakeup.daemon"
	plistPath  = "/Library/LaunchDaemons/" + plistName + ".plist"
	plistSrc   = "com.wakeup.daemon.plist"
	configPath = "/etc/wakeup/config.toml"
)

var scanner = bufio.NewScanner(os.Stdin)

func runInstall() {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "install requires sudo: sudo wakeup install")
		os.Exit(1)
	}

	fmt.Println("=== wakeup-macos installer ===")
	fmt.Println()

	// Step 1: Interactive config
	cfg := interactiveConfig()

	// Step 2: Write config file
	fmt.Println()
	fmt.Printf("[1/5] Writing config -> %s\n", configPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		fatal("create config dir: %v", err)
	}
	configContent := fmt.Sprintf(`# wakeup daemon configuration (auto-generated)

worker_url = %q
token = %q
device_id = %q
check_interval = %q
default_duration = %q
`, cfg.WorkerURL, cfg.Token, cfg.DeviceID, cfg.CheckInterval.String(), cfg.DefaultDuration.String())

	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		fatal("write config: %v", err)
	}

	// Step 3: Copy binary
	self, err := os.Executable()
	if err != nil {
		fatal("cannot find self: %v", err)
	}
	self, _ = filepath.EvalSymlinks(self)

	fmt.Printf("[2/5] Installing binary -> %s\n", binPath)
	input, err := os.ReadFile(self)
	if err != nil {
		fatal("read binary: %v", err)
	}
	if err := os.WriteFile(binPath, input, 0755); err != nil {
		fatal("write binary: %v", err)
	}

	// Step 4: Install plist
	exec.Command("launchctl", "bootout", "system/"+plistName).Run()

	plistData, err := findPlist(self)
	if err != nil {
		fatal("find plist: %v", err)
	}
	fmt.Printf("[3/5] Installing plist -> %s\n", plistPath)
	if err := os.WriteFile(plistPath, plistData, 0644); err != nil {
		fatal("write plist: %v", err)
	}

	// Step 5: Setup pmset
	fmt.Println("[4/5] Setting up pmset repeat wake (daily 08:00 fallback)")
	power.SetupRepeatWake()

	// Step 6: Load daemon
	fmt.Println("[5/5] Loading daemon...")
	out, err := exec.Command("launchctl", "bootstrap", "system", plistPath).CombinedOutput()
	if err != nil {
		out, err = exec.Command("launchctl", "load", "-w", plistPath).CombinedOutput()
		if err != nil {
			fatal("launchctl load: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}

	fmt.Println()
	fmt.Println("=== Installation complete ===")
	fmt.Println()
	fmt.Printf("  Device ID:      %s\n", cfg.DeviceID)
	fmt.Printf("  Check interval: %s\n", cfg.CheckInterval)
	fmt.Printf("  Wake duration:  %s\n", cfg.DefaultDuration)
	fmt.Printf("  Config:         %s\n", configPath)
	fmt.Printf("  Logs:           /var/log/wakeup.log\n")
	fmt.Println()
	fmt.Println("To wake this Mac from another device:")
	fmt.Printf("  wakeup send %s\n", cfg.DeviceID)
	fmt.Println()
	fmt.Println("Useful commands:")
	fmt.Println("  sudo launchctl kickstart -k system/com.wakeup.daemon  # restart daemon")
	fmt.Println("  tail -f /var/log/wakeup.log                           # view logs")
	fmt.Println("  wakeup status                                         # check all devices")
}

func interactiveConfig() *config.Config {
	cfg := &config.Config{
		CheckInterval:   15 * time.Minute,
		DefaultDuration: 30 * time.Minute,
	}

	// Try loading existing config
	existing, err := loadExistingConfig()
	if err == nil && existing != nil {
		fmt.Println("Found existing config at " + configPath)
		fmt.Println()
		cfg = existing
	}

	// Worker URL
	cfg.WorkerURL = prompt(
		"Cloudflare Worker URL",
		cfg.WorkerURL,
		"e.g. https://wakeup-worker.your-subdomain.workers.dev",
		func(s string) error {
			if !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "http://") {
				return fmt.Errorf("must start with https://")
			}
			return nil
		},
	)

	// Token
	defaultToken := cfg.Token
	if defaultToken == "" {
		defaultToken = generateToken()
	}
	cfg.Token = prompt(
		"Auth token",
		defaultToken,
		"random token for API auth (press Enter to use generated value)",
		nil,
	)

	// Device ID
	hostname, _ := os.Hostname()
	defaultDevice := cfg.DeviceID
	if defaultDevice == "" {
		defaultDevice = sanitizeDeviceID(hostname)
	}
	cfg.DeviceID = prompt(
		"Device ID for this Mac",
		defaultDevice,
		"unique name like: office, home-mini, home-mbp",
		func(s string) error {
			if strings.ContainsAny(s, " /\\?#") {
				return fmt.Errorf("cannot contain spaces or special characters")
			}
			return nil
		},
	)

	// Check interval
	intervalStr := prompt(
		"Check interval",
		cfg.CheckInterval.String(),
		"how often to check for wake signals (e.g. 10m, 15m, 30m)",
		func(s string) error {
			d, err := time.ParseDuration(s)
			if err != nil {
				return err
			}
			if d < 1*time.Minute {
				return fmt.Errorf("must be at least 1m")
			}
			return nil
		},
	)
	cfg.CheckInterval, _ = time.ParseDuration(intervalStr)

	// Default duration
	durationStr := prompt(
		"Default wake duration",
		cfg.DefaultDuration.String(),
		"how long to stay awake after receiving signal (e.g. 30m, 1h)",
		func(s string) error {
			d, err := time.ParseDuration(s)
			if err != nil {
				return err
			}
			if d < 1*time.Minute {
				return fmt.Errorf("must be at least 1m")
			}
			return nil
		},
	)
	cfg.DefaultDuration, _ = time.ParseDuration(durationStr)

	// Confirm
	fmt.Println()
	fmt.Println("--- Configuration Summary ---")
	fmt.Printf("  Worker URL:     %s\n", cfg.WorkerURL)
	fmt.Printf("  Token:          %s\n", cfg.Token)
	fmt.Printf("  Device ID:      %s\n", cfg.DeviceID)
	fmt.Printf("  Check interval: %s\n", cfg.CheckInterval)
	fmt.Printf("  Wake duration:  %s\n", cfg.DefaultDuration)
	fmt.Println()

	confirm := prompt("Proceed with installation?", "y", "[y/n]", nil)
	if !strings.HasPrefix(strings.ToLower(confirm), "y") {
		fmt.Println("Installation cancelled.")
		os.Exit(0)
	}

	return cfg
}

func prompt(label, defaultVal, hint string, validate func(string) error) string {
	for {
		if defaultVal != "" {
			fmt.Printf("  %s [%s]: ", label, defaultVal)
		} else {
			fmt.Printf("  %s: ", label)
		}
		if hint != "" && defaultVal == "" {
			fmt.Printf("(%s) ", hint)
		}

		scanner.Scan()
		input := strings.TrimSpace(scanner.Text())

		if input == "" {
			if defaultVal != "" {
				return defaultVal
			}
			fmt.Printf("    -> required, %s\n", hint)
			continue
		}

		if validate != nil {
			if err := validate(input); err != nil {
				fmt.Printf("    -> %v\n", err)
				continue
			}
		}

		return input
	}
}

func generateToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func sanitizeDeviceID(hostname string) string {
	s := strings.ToLower(hostname)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.TrimSuffix(s, "-local")
	if s == "" {
		s = "my-mac"
	}
	return s
}

func loadExistingConfig() (*config.Config, error) {
	if _, err := os.Stat(configPath); err != nil {
		return nil, err
	}
	return config.Load()
}

func runUninstall() {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "uninstall requires sudo: sudo wakeup uninstall")
		os.Exit(1)
	}

	fmt.Println("=== wakeup-macos uninstaller ===")
	fmt.Println()

	// Unload daemon
	fmt.Println("[1/4] Unloading daemon...")
	exec.Command("launchctl", "bootout", "system/"+plistName).Run()
	exec.Command("launchctl", "unload", plistPath).Run()

	// Clear pmset repeat
	fmt.Println("[2/4] Clearing pmset repeat wake")
	power.ClearRepeatWake()

	// Remove plist and binary
	fmt.Println("[3/4] Removing files...")
	os.Remove(plistPath)
	os.Remove(binPath)

	// Ask about config
	fmt.Println("[4/4] Config file")
	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("  Keep config at %s? [y/n] (default: y): ", configPath)
		scanner.Scan()
		input := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(strings.ToLower(input), "n") {
			os.Remove(configPath)
			os.Remove(filepath.Dir(configPath))
			fmt.Println("  Config removed.")
		} else {
			fmt.Println("  Config kept.")
		}
	}

	fmt.Println()
	fmt.Println("Uninstalled successfully.")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func findPlist(selfPath string) ([]byte, error) {
	// Look for plist relative to binary location (dev mode)
	dir := filepath.Dir(selfPath)
	candidates := []string{
		filepath.Join(dir, plistSrc),
		filepath.Join(dir, "..", plistSrc),
		filepath.Join(dir, "..", "..", plistSrc),
		plistSrc,
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("cannot find %s (searched near %s)", plistSrc, selfPath)
}

// parseDuration parses a duration string, supporting plain minutes as a number.
func parseDuration(s string) (time.Duration, error) {
	// Try as plain number (minutes)
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Minute, nil
	}
	return time.ParseDuration(s)
}
