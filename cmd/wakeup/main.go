package main

import (
	"bufio"
	"context"
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
	case "config":
		runConfig()
	case "cancel":
		runCancel()
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
  wakeup config push [--device <id>]  Push local config to remote (global or device)
  wakeup config get [--device <id>]   Get remote config (global or device)
  wakeup config delete --device <id>  Delete device-specific remote config
  wakeup config show                  Show effective merged config with sources
  wakeup cancel                        Cancel all active caffeinate sessions
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

func runCancel() {
	killed, err := power.KillAllCaffeinate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cancel error: %v\n", err)
		os.Exit(1)
	}
	if killed == 0 {
		fmt.Println("no active caffeinate sessions found")
		return
	}
	fmt.Printf("cancelled %d caffeinate session(s)\n", killed)
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

	ctx := context.Background()
	if all {
		err = client.SendAll(ctx, duration)
		if err != nil {
			fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Wake signal sent to all devices (duration: %s)\n", duration)
	} else {
		err = client.Send(ctx, deviceID, duration)
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
	status, err := client.Status(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "status failed: %v\n", err)
		os.Exit(1)
	}

	// Show local power state and effective interval
	onAC := power.IsOnACPower()
	powerStr := "battery"
	effectiveInterval := cfg.BatteryCheckInterval
	if onAC {
		powerStr = "AC"
		effectiveInterval = cfg.ACCheckInterval
	}
	fmt.Printf("  Local power: %s (check interval: %s)\n", powerStr, effectiveInterval)
	fmt.Println()

	if len(status) == 0 {
		fmt.Println("No devices registered")
		return
	}

	// Consider a device "online" if last seen within 2x the longer interval
	onlineThreshold := cfg.BatteryCheckInterval * 2
	var online, offline, pending int

	for id, s := range status {
		state := "offline"
		if s.LastSeen > 0 {
			since := time.Since(time.UnixMilli(s.LastSeen))
			if since < onlineThreshold {
				state = "online"
				online++
			} else {
				offline++
			}
		} else {
			offline++
		}

		if s.PendingWake {
			state += " | PENDING WAKE"
			pending++
		}

		lastSeen := "never"
		if s.LastSeen > 0 {
			t := time.UnixMilli(s.LastSeen)
			lastSeen = time.Since(t).Truncate(time.Second).String() + " ago"
		}
		fmt.Printf("  %-20s  %-25s  (last seen: %s)\n", id, state, lastSeen)
	}

	fmt.Println()
	fmt.Printf("  %d online, %d offline, %d total", online, offline, len(status))
	if pending > 0 {
		fmt.Printf(", %d pending wake", pending)
	}
	fmt.Println()
}

func runDevices() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	client := cloud.NewClient(cfg.WorkerURL, cfg.Token)
	devices, err := client.Devices(context.Background())
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

func runConfig() {
	args := os.Args[2:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: wakeup config <push|get|delete|show> [--device <id>]")
		os.Exit(1)
	}

	action := args[0]
	deviceID := parseDeviceFlag(args[1:])

	switch action {
	case "push":
		runConfigPush(deviceID)
	case "get":
		runConfigGet(deviceID)
	case "delete":
		runConfigDelete(deviceID)
	case "show":
		runConfigShow()
	default:
		fmt.Fprintf(os.Stderr, "unknown config action: %s\n", action)
		fmt.Fprintln(os.Stderr, "usage: wakeup config <push|get|delete|show> [--device <id>]")
		os.Exit(1)
	}
}

func parseDeviceFlag(args []string) string {
	for i, arg := range args {
		if arg == "--device" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func runConfigPush(deviceID string) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	client := cloud.NewClient(cfg.WorkerURL, cfg.Token)
	remote := config.ToRemoteConfig(cfg)

	ctx := context.Background()
	if deviceID != "" {
		resp, err := client.PushDeviceConfig(ctx, deviceID, toCloudRemoteConfig(remote))
		if err != nil {
			fmt.Fprintf(os.Stderr, "push failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Device config pushed for %s (version: %s)\n", deviceID, resp.Version)
	} else {
		resp, err := client.PushGlobalConfig(ctx, toCloudRemoteConfig(remote))
		if err != nil {
			fmt.Fprintf(os.Stderr, "push failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Global config pushed (version: %s)\n", resp.Version)
	}

	printRemoteConfig(remote)
}

func runConfigGet(deviceID string) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	client := cloud.NewClient(cfg.WorkerURL, cfg.Token)
	ctx := context.Background()

	var resp *cloud.ConfigResponse
	if deviceID != "" {
		resp, err = client.GetDeviceConfig(ctx, deviceID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "get failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Device config for %s (version: %s):\n", deviceID, resp.Version)
	} else {
		resp, err = client.GetGlobalConfig(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "get failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Global config (version: %s):\n", resp.Version)
	}

	printCloudRemoteConfig(&resp.Config)
}

func runConfigDelete(deviceID string) {
	if deviceID == "" {
		fmt.Fprintln(os.Stderr, "usage: wakeup config delete --device <id>")
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	client := cloud.NewClient(cfg.WorkerURL, cfg.Token)
	if err := client.DeleteDeviceConfig(context.Background(), deviceID); err != nil {
		fmt.Fprintf(os.Stderr, "delete failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Device config deleted for %s\n", deviceID)
}

func runConfigShow() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	client := cloud.NewClient(cfg.WorkerURL, cfg.Token)
	ctx := context.Background()

	// Try to fetch remote config
	var globalCfg, deviceCfg *cloud.RemoteConfig
	globalResp, err := client.GetGlobalConfig(ctx)
	if err == nil {
		globalCfg = &globalResp.Config
	}
	if cfg.DeviceID != "" {
		deviceResp, err := client.GetDeviceConfig(ctx, cfg.DeviceID)
		if err == nil {
			deviceCfg = &deviceResp.Config
		}
	}

	fmt.Println("Effective config (merged):")
	fmt.Println()

	// Apply merge chain: local -> global -> device
	merged := cfg
	if globalCfg != nil {
		remoteCfg := cloudToConfigRemote(globalCfg)
		merged, _ = config.MergeRemote(merged, remoteCfg)
	}
	if deviceCfg != nil {
		remoteCfg := cloudToConfigRemote(deviceCfg)
		merged, _ = config.MergeRemote(merged, remoteCfg)
	}

	// Print each field with source annotation
	printFieldWithSource("worker_url", merged.WorkerURL, "local (always)")
	printFieldWithSource("token", merged.Token, "local (always)")
	printFieldWithSource("device_id", merged.DeviceID, "local (always)")
	printFieldWithSource("check_interval", merged.CheckInterval.String(),
		fieldSource(cfg.CheckInterval, merged.CheckInterval, globalCfg, deviceCfg, "check_interval"))
	printFieldWithSource("default_duration", merged.DefaultDuration.String(),
		fieldSource(cfg.DefaultDuration, merged.DefaultDuration, globalCfg, deviceCfg, "default_duration"))
	printFieldWithSource("ac_check_interval", merged.ACCheckInterval.String(),
		fieldSource(cfg.ACCheckInterval, merged.ACCheckInterval, globalCfg, deviceCfg, "ac_check_interval"))
	printFieldWithSource("battery_check_interval", merged.BatteryCheckInterval.String(),
		fieldSource(cfg.BatteryCheckInterval, merged.BatteryCheckInterval, globalCfg, deviceCfg, "battery_check_interval"))
	printFieldWithSource("enable_darkwake_detection", fmt.Sprintf("%v", merged.EnableDarkwakeDetection),
		darkwakeSource(cfg.EnableDarkwakeDetection, merged.EnableDarkwakeDetection, globalCfg, deviceCfg))
	printFieldWithSource("wake_detect_interval", merged.WakeDetectInterval.String(),
		fieldSource(cfg.WakeDetectInterval, merged.WakeDetectInterval, globalCfg, deviceCfg, "wake_detect_interval"))
}

func printFieldWithSource(name, value, source string) {
	fmt.Printf("  %-28s  %-16s  (%s)\n", name, value, source)
}

func fieldSource(localVal, mergedVal time.Duration, global, device *cloud.RemoteConfig, field string) string {
	if device != nil {
		var dv int
		switch field {
		case "check_interval":
			dv = device.CheckInterval
		case "default_duration":
			dv = device.DefaultDuration
		case "ac_check_interval":
			dv = device.ACCheckInterval
		case "battery_check_interval":
			dv = device.BatteryCheckInterval
		case "wake_detect_interval":
			dv = device.WakeDetectInterval
		}
		if dv > 0 && time.Duration(dv)*time.Second == mergedVal {
			return "remote-device"
		}
	}
	if global != nil {
		var gv int
		switch field {
		case "check_interval":
			gv = global.CheckInterval
		case "default_duration":
			gv = global.DefaultDuration
		case "ac_check_interval":
			gv = global.ACCheckInterval
		case "battery_check_interval":
			gv = global.BatteryCheckInterval
		case "wake_detect_interval":
			gv = global.WakeDetectInterval
		}
		if gv > 0 && time.Duration(gv)*time.Second == mergedVal {
			return "remote-global"
		}
	}
	return "local"
}

func darkwakeSource(localVal, mergedVal bool, global, device *cloud.RemoteConfig) string {
	if device != nil && device.EnableDarkwakeDetection != nil && *device.EnableDarkwakeDetection == mergedVal {
		return "remote-device"
	}
	if global != nil && global.EnableDarkwakeDetection != nil && *global.EnableDarkwakeDetection == mergedVal {
		return "remote-global"
	}
	return "local"
}

// Conversion helpers between config.RemoteConfig and cloud.RemoteConfig
func toCloudRemoteConfig(r *config.RemoteConfig) *cloud.RemoteConfig {
	return &cloud.RemoteConfig{
		CheckInterval:           r.CheckInterval,
		DefaultDuration:         r.DefaultDuration,
		ACCheckInterval:         r.ACCheckInterval,
		BatteryCheckInterval:    r.BatteryCheckInterval,
		EnableDarkwakeDetection: r.EnableDarkwakeDetection,
		WakeDetectInterval:      r.WakeDetectInterval,
	}
}

func cloudToConfigRemote(r *cloud.RemoteConfig) *config.RemoteConfig {
	return &config.RemoteConfig{
		CheckInterval:           r.CheckInterval,
		DefaultDuration:         r.DefaultDuration,
		ACCheckInterval:         r.ACCheckInterval,
		BatteryCheckInterval:    r.BatteryCheckInterval,
		EnableDarkwakeDetection: r.EnableDarkwakeDetection,
		WakeDetectInterval:      r.WakeDetectInterval,
	}
}

func printRemoteConfig(r *config.RemoteConfig) {
	fmt.Println()
	if r.CheckInterval > 0 {
		fmt.Printf("  check_interval:            %s\n", (time.Duration(r.CheckInterval) * time.Second).String())
	}
	if r.DefaultDuration > 0 {
		fmt.Printf("  default_duration:          %s\n", (time.Duration(r.DefaultDuration) * time.Second).String())
	}
	if r.ACCheckInterval > 0 {
		fmt.Printf("  ac_check_interval:         %s\n", (time.Duration(r.ACCheckInterval) * time.Second).String())
	}
	if r.BatteryCheckInterval > 0 {
		fmt.Printf("  battery_check_interval:    %s\n", (time.Duration(r.BatteryCheckInterval) * time.Second).String())
	}
	if r.EnableDarkwakeDetection != nil {
		fmt.Printf("  enable_darkwake_detection: %v\n", *r.EnableDarkwakeDetection)
	}
	if r.WakeDetectInterval > 0 {
		fmt.Printf("  wake_detect_interval:      %s\n", (time.Duration(r.WakeDetectInterval) * time.Second).String())
	}
}

func printCloudRemoteConfig(r *cloud.RemoteConfig) {
	fmt.Println()
	if r.CheckInterval > 0 {
		fmt.Printf("  check_interval:            %s\n", (time.Duration(r.CheckInterval) * time.Second).String())
	}
	if r.DefaultDuration > 0 {
		fmt.Printf("  default_duration:          %s\n", (time.Duration(r.DefaultDuration) * time.Second).String())
	}
	if r.ACCheckInterval > 0 {
		fmt.Printf("  ac_check_interval:         %s\n", (time.Duration(r.ACCheckInterval) * time.Second).String())
	}
	if r.BatteryCheckInterval > 0 {
		fmt.Printf("  battery_check_interval:    %s\n", (time.Duration(r.BatteryCheckInterval) * time.Second).String())
	}
	if r.EnableDarkwakeDetection != nil {
		fmt.Printf("  enable_darkwake_detection: %v\n", *r.EnableDarkwakeDetection)
	}
	if r.WakeDetectInterval > 0 {
		fmt.Printf("  wake_detect_interval:      %s\n", (time.Duration(r.WakeDetectInterval) * time.Second).String())
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

	// Step 2: Write config files (both user and system)
	configContent := fmt.Sprintf(`# wakeup daemon configuration (auto-generated)

worker_url = %q
token = %q
device_id = %q
check_interval = %q
default_duration = %q
ac_check_interval = %q
battery_check_interval = %q
enable_darkwake_detection = %v
wake_detect_interval = %q
`, cfg.WorkerURL, cfg.Token, cfg.DeviceID, cfg.CheckInterval.String(), cfg.DefaultDuration.String(),
		cfg.ACCheckInterval.String(), cfg.BatteryCheckInterval.String(),
		cfg.EnableDarkwakeDetection, cfg.WakeDetectInterval.String())

	fmt.Println()
	// Write system config (for launchd daemon running as root)
	fmt.Printf("[1/5] Writing config -> %s\n", configPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		fatal("create config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		fatal("write config: %v", err)
	}

	// Write user config (for CLI commands without sudo)
	userConfigDir, err := config.ConfigDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not create user config dir: %v\n", err)
	} else {
		userConfigPath := filepath.Join(userConfigDir, "config.toml")
		fmt.Printf("         Writing config -> %s\n", userConfigPath)
		// Determine the real user (sudo sets SUDO_UID)
		if sudoUID := os.Getenv("SUDO_UID"); sudoUID != "" {
			uid, _ := strconv.Atoi(sudoUID)
			gid := uid // default; overridden below if SUDO_GID is set
			if sudoGID := os.Getenv("SUDO_GID"); sudoGID != "" {
				gid, _ = strconv.Atoi(sudoGID)
			}
			if err := os.WriteFile(userConfigPath, []byte(configContent), 0600); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not write user config: %v\n", err)
			} else {
				os.Chown(userConfigPath, uid, gid)
				os.Chown(userConfigDir, uid, gid)
			}
		} else {
			if err := os.WriteFile(userConfigPath, []byte(configContent), 0600); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not write user config: %v\n", err)
			}
		}
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
	fmt.Printf("  Device ID:        %s\n", cfg.DeviceID)
	fmt.Printf("  AC interval:      %s\n", cfg.ACCheckInterval)
	fmt.Printf("  Battery interval: %s\n", cfg.BatteryCheckInterval)
	fmt.Printf("  Darkwake detect:  %v\n", cfg.EnableDarkwakeDetection)
	fmt.Printf("  Wake duration:    %s\n", cfg.DefaultDuration)
	fmt.Printf("  Config (daemon):  %s\n", configPath)
	fmt.Printf("  Config (user):   ~/.config/wakeup/config.toml\n")
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
		CheckInterval:        15 * time.Minute,
		DefaultDuration:      30 * time.Minute,
		ACCheckInterval:      2 * time.Minute,
		BatteryCheckInterval: 15 * time.Minute,
		WakeDetectInterval:   30 * time.Second,
	}

	// Try loading existing config
	existing, err := loadExistingConfig()
	if err == nil && existing != nil {
		fmt.Println("Found existing config at " + configPath)
		fmt.Println()
		cfg = existing
	}

	// Step 1: Always ask for the three essential fields first
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

	// Step 2: Try fetching remote config from the Worker
	fmt.Println()
	fmt.Println("Checking for remote config...")
	remoteApplied := tryApplyRemoteConfig(cfg)

	if remoteApplied {
		// Path A: remote config found and applied
		fmt.Println()
		fmt.Println("--- Configuration Summary (from remote) ---")
		fmt.Printf("  Worker URL:        %s\n", cfg.WorkerURL)
		fmt.Printf("  Token:             %s\n", cfg.Token)
		fmt.Printf("  Device ID:         %s\n", cfg.DeviceID)
		fmt.Printf("  Check interval:    %s\n", cfg.CheckInterval)
		fmt.Printf("  AC interval:       %s\n", cfg.ACCheckInterval)
		fmt.Printf("  Battery interval:  %s\n", cfg.BatteryCheckInterval)
		fmt.Printf("  Darkwake detect:   %v\n", cfg.EnableDarkwakeDetection)
		if cfg.EnableDarkwakeDetection {
			fmt.Printf("  Wake detect int:   %s\n", cfg.WakeDetectInterval)
		}
		fmt.Printf("  Wake duration:     %s\n", cfg.DefaultDuration)
		fmt.Println()

		confirm := prompt("Proceed with installation?", "y", "[y/n]", nil)
		if !strings.HasPrefix(strings.ToLower(confirm), "y") {
			fmt.Println("Installation cancelled.")
			os.Exit(0)
		}
		return cfg
	}

	// Path B: no remote config — prompt each remaining field
	fmt.Println("No remote config found. Please configure each setting.")
	fmt.Println("(Press Enter to keep the current/default value)")
	fmt.Println()

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

	// AC check interval
	acIntervalStr := prompt(
		"AC power check interval",
		cfg.ACCheckInterval.String(),
		"check interval when plugged in (e.g. 2m, 5m)",
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
	cfg.ACCheckInterval, _ = time.ParseDuration(acIntervalStr)

	// Battery check interval
	batIntervalStr := prompt(
		"Battery check interval",
		cfg.BatteryCheckInterval.String(),
		"check interval on battery (e.g. 15m, 30m)",
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
	cfg.BatteryCheckInterval, _ = time.ParseDuration(batIntervalStr)

	// Darkwake detection
	darkwakeStr := prompt(
		"Enable darkwake detection (experimental)",
		boolToYN(cfg.EnableDarkwakeDetection),
		"detect sleep/wake events for faster response [y/n]",
		func(s string) error {
			s = strings.ToLower(s)
			if s != "y" && s != "n" && s != "yes" && s != "no" {
				return fmt.Errorf("enter y or n")
			}
			return nil
		},
	)
	cfg.EnableDarkwakeDetection = strings.HasPrefix(strings.ToLower(darkwakeStr), "y")

	if cfg.EnableDarkwakeDetection {
		wakeDetectStr := prompt(
			"Wake detect interval",
			cfg.WakeDetectInterval.String(),
			"how often to check for time jumps (e.g. 30s, 1m)",
			func(s string) error {
				d, err := time.ParseDuration(s)
				if err != nil {
					return err
				}
				if d < 10*time.Second {
					return fmt.Errorf("must be at least 10s")
				}
				return nil
			},
		)
		cfg.WakeDetectInterval, _ = time.ParseDuration(wakeDetectStr)
	}

	// Confirm
	fmt.Println()
	fmt.Println("--- Configuration Summary ---")
	fmt.Printf("  Worker URL:        %s\n", cfg.WorkerURL)
	fmt.Printf("  Token:             %s\n", cfg.Token)
	fmt.Printf("  Device ID:         %s\n", cfg.DeviceID)
	fmt.Printf("  Check interval:    %s\n", cfg.CheckInterval)
	fmt.Printf("  AC interval:       %s\n", cfg.ACCheckInterval)
	fmt.Printf("  Battery interval:  %s\n", cfg.BatteryCheckInterval)
	fmt.Printf("  Darkwake detect:   %v\n", cfg.EnableDarkwakeDetection)
	if cfg.EnableDarkwakeDetection {
		fmt.Printf("  Wake detect int:   %s\n", cfg.WakeDetectInterval)
	}
	fmt.Printf("  Wake duration:     %s\n", cfg.DefaultDuration)
	fmt.Println()

	confirm := prompt("Proceed with installation?", "y", "[y/n]", nil)
	if !strings.HasPrefix(strings.ToLower(confirm), "y") {
		fmt.Println("Installation cancelled.")
		os.Exit(0)
	}

	return cfg
}

// tryApplyRemoteConfig attempts to fetch global and device-specific remote config
// from the Worker and merge them into cfg. Returns true if any remote config was found.
func tryApplyRemoteConfig(cfg *config.Config) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client := cloud.NewClient(cfg.WorkerURL, cfg.Token)

	var hasRemote bool

	// Fetch global config
	globalResp, err := client.GetGlobalConfig(ctx)
	if err == nil && !isEmptyRemoteConfig(&globalResp.Config) {
		fmt.Println("  Found global remote config.")
		applyCloudRemoteConfig(cfg, &globalResp.Config)
		hasRemote = true
	}

	// Fetch device-specific config (overrides global)
	deviceResp, err := client.GetDeviceConfig(ctx, cfg.DeviceID)
	if err == nil && !isEmptyRemoteConfig(&deviceResp.Config) {
		fmt.Printf("  Found device config for %q.\n", cfg.DeviceID)
		applyCloudRemoteConfig(cfg, &deviceResp.Config)
		hasRemote = true
	}

	if !hasRemote {
		if err != nil {
			fmt.Printf("  Could not reach Worker: %v\n", err)
		}
	}

	return hasRemote
}

// applyCloudRemoteConfig applies non-zero fields from a cloud.RemoteConfig onto cfg.
func applyCloudRemoteConfig(cfg *config.Config, rc *cloud.RemoteConfig) {
	if rc.CheckInterval > 0 {
		cfg.CheckInterval = time.Duration(rc.CheckInterval) * time.Second
	}
	if rc.DefaultDuration > 0 {
		cfg.DefaultDuration = time.Duration(rc.DefaultDuration) * time.Second
	}
	if rc.ACCheckInterval > 0 {
		cfg.ACCheckInterval = time.Duration(rc.ACCheckInterval) * time.Second
	}
	if rc.BatteryCheckInterval > 0 {
		cfg.BatteryCheckInterval = time.Duration(rc.BatteryCheckInterval) * time.Second
	}
	if rc.EnableDarkwakeDetection != nil {
		cfg.EnableDarkwakeDetection = *rc.EnableDarkwakeDetection
	}
	if rc.WakeDetectInterval > 0 {
		cfg.WakeDetectInterval = time.Duration(rc.WakeDetectInterval) * time.Second
	}
}

// isEmptyRemoteConfig returns true if all fields in the remote config are zero/nil.
func isEmptyRemoteConfig(rc *cloud.RemoteConfig) bool {
	return rc.CheckInterval == 0 &&
		rc.DefaultDuration == 0 &&
		rc.ACCheckInterval == 0 &&
		rc.BatteryCheckInterval == 0 &&
		rc.EnableDarkwakeDetection == nil &&
		rc.WakeDetectInterval == 0
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

func boolToYN(b bool) string {
	if b {
		return "y"
	}
	return "n"
}
