# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.1] - 2026-04-15

### Added

- Darkwake detection (experimental): opt-in `enable_darkwake_detection` config flag
- When enabled, daemon detects system wake events (including darkwake) via wall clock time jumps
- Opportunistic signal checks during darkwake can reduce effective latency to ~45-120 seconds
- New config fields: `enable_darkwake_detection` (default false), `wake_detect_interval` (default 30s)
- Uses `time.Now().Round(0)` (wall clock) instead of monotonic clock — Go's monotonic clock stops during macOS sleep

## [0.2.0] - 2026-04-15

### Added

- Adaptive check intervals based on power state: shorter interval on AC power (default 2m), longer on battery (default 15m)
- Power state detection via `pmset -g ps` (`IsOnACPower()`)
- New config fields: `ac_check_interval`, `battery_check_interval` with environment variable overrides
- `status` command now shows local power state and effective check interval
- Context cancellation support for HTTP requests — daemon shuts down faster

### Changed

- Cloud client methods now accept `context.Context` for cancellation support
- Daemon uses channel-based session management instead of direct goroutine mutation (fixes data race on `d.session`)
- Session monitor goroutine no longer directly modifies daemon state — communicates via `sessionDone` channel
- Install flow now prompts for AC and battery check intervals

### Fixed

- Data race on `d.session` between main goroutine and caffeinate session monitor goroutine
- Session pointer aliasing: replacing a caffeinate session no longer risks orphaning the new session
- `scheduleNextWake()` now uses current power state at call time, avoiding stale interval values

## [0.1.1] - 2026-04-15

### Changed

- Config lookup now prefers user directory (`~/.config/wakeup/config.toml`) over system directory (`/etc/wakeup/config.toml`), so CLI commands like `send`, `status`, `devices` no longer require sudo
- `install` command now writes config to both user and system paths; user config file ownership is corrected via `SUDO_UID`/`SUDO_GID`
- Installation summary now shows both config paths (daemon and user)

## [0.1.0] - 2026-04-15

### Added

- Remote wake daemon for macOS via Cloudflare Workers
- Scheduled hardware RTC wake using `pmset relative wake` (default every 15 minutes)
- Wake signal check through Cloudflare Worker (KV storage) after each wake
- `caffeinate` session to keep Mac awake for a specified duration upon receiving signal (default 30 minutes)
- CLI commands: `daemon`, `send`, `status`, `devices`, `install`, `uninstall`, `version`
- Cloudflare Worker endpoints: wake/check/status/devices
- Device online/offline status query
- TOML config file with environment variable overrides
- `launchd` plist for system-level daemon management
- Makefile build system (build, test, install, dev, deploy-worker, clean)
- Quick install script `scripts/install.sh`

### Documentation

- Full README with installation, configuration, and usage instructions
- Detailed Worker deployment guide
- Example config files `config.example.toml` and `wrangler.toml.example`

[0.2.1]: https://github.com/d0zingcat/wakeup-macos/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/d0zingcat/wakeup-macos/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/d0zingcat/wakeup-macos/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/d0zingcat/wakeup-macos/releases/tag/v0.1.0
