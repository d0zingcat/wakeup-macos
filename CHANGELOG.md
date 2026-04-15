# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[0.1.1]: https://github.com/d0zingcat/wakeup-macos/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/d0zingcat/wakeup-macos/releases/tag/v0.1.0
