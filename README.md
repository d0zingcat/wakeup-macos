# wakeup-macos

Remotely wake your sleeping Mac via Cloudflare Workers — no VPN, no extra hardware.

## Problem

When your Mac sleeps, Tailscale (WireGuard) disconnects. Without a local network VPN, you can't use Wake-on-LAN to bring it back. But you also don't want to disable sleep entirely — that wears out your hardware.

## How It Works

```
You (phone/laptop)                    Cloudflare Worker              Your Mac (sleeping)
       |                                     |                              |
       |  wakeup send office                 |                              |
       | ----------------------------------> |                              |
       |        store wake signal in KV      |                              |
       |                                     |     (pmset wakes Mac every   |
       |                                     |      15 min via hardware RTC)|
       |                                     |                              |
       |                                     | <--- GET /check/office ------|
       |                                     | --- wake signal found! ----> |
       |                                     |                              |
       |                                     |     caffeinate keeps Mac     |
       |                                     |     awake for 30 min        |
       |                                     |     Tailscale reconnects     |
       |                                     |                              |
       |  ssh / remote desktop works now     |                              |
       | <=============================================================> |
```

1. A lightweight daemon runs on your Mac, managed by launchd
2. When the Mac sleeps, `pmset relative wake` schedules a hardware RTC wake (every ~2 min on AC, ~15 min on battery)
3. On each wake, the daemon checks a Cloudflare Worker for a wake signal
4. If a signal is found, `caffeinate` keeps the Mac awake for the requested duration (default 30 min)
5. Tailscale reconnects automatically, and you can access your Mac remotely
6. When the duration expires, the Mac goes back to sleep naturally

## Features

- **No extra hardware** — pure software solution using macOS native tools (`pmset`, `caffeinate`)
- **Multi-device support** — manage multiple Macs with unique device IDs, wake them individually or all at once
- **Minimal power impact** — adaptive intervals: ~2 min on AC power, 15 min on battery
- **Interactive installer** — guided setup with config generation
- **Single binary** — daemon and CLI in one Go binary

## Prerequisites

- macOS (Apple Silicon or Intel)
- [Go](https://go.dev/) 1.21+ (to build)
- [Bun](https://bun.sh/) (to deploy the Cloudflare Worker)
- A free [Cloudflare](https://cloudflare.com/) account

## Quick Start

### 1. Deploy the Cloudflare Worker

```bash
cd worker
bun install
```

Login to Cloudflare (opens browser for authorization):

```bash
bunx wrangler login
```

Create a KV namespace (Cloudflare Workers KV is a key-value store used to hold wake signals, you need to create a namespace first via wrangler CLI):

```bash
bunx wrangler kv namespace create WAKEUP_KV
```

This outputs something like:

```
🌀 Creating namespace with title "wakeup-worker-WAKEUP_KV"
✨ Success!
Add the following to your configuration file in your kv_namespaces array:
{ binding = "WAKEUP_KV", id = "a1b2c3d4e5f6g7h8i9j0..." }
```

Copy the example config and fill in your values (`wrangler.toml` is gitignored to avoid leaking secrets):

```bash
cp wrangler.toml.example wrangler.toml
vim wrangler.toml
```

```toml
[[kv_namespaces]]
binding = "WAKEUP_KV"
id = "a1b2c3d4e5f6g7h8i9j0..."  # paste your ID from above

[vars]
AUTH_TOKEN = "your-random-token"  # generate one: openssl rand -hex 16
```

Deploy:

```bash
bun run deploy
```

Output:

```
Published wakeup-worker (x.xx sec)
  https://wakeup-worker.your-subdomain.workers.dev
```

Verify it works:

```bash
# Wrong token → 404
curl https://wakeup-worker.your-subdomain.workers.dev/wrong-token/status

# Correct token → {"devices":{}}
curl https://wakeup-worker.your-subdomain.workers.dev/your-random-token/status
```

Save the Worker URL and token — you'll need both when installing the daemon.

### 2. Build and Install on Your Mac

```bash
make build
sudo ./wakeup install
```

The interactive installer will guide you through:

```
=== wakeup-macos installer ===

  Cloudflare Worker URL []: https://wakeup-worker.your-subdomain.workers.dev
  Auth token [a1b2c3d4e5f6...]: (press Enter to use generated token)
  Device ID for this Mac [your-hostname]: office
  Check interval [15m]:
  Default wake duration [30m]:

--- Configuration Summary ---
  Worker URL:     https://wakeup-worker.your-subdomain.workers.dev
  Token:          a1b2c3d4e5f6...
  Device ID:      office
  Check interval: 15m
  Wake duration:  30m

  Proceed with installation? [y]:
```

> **Important:** Use the same `AUTH_TOKEN` value in both `wrangler.toml` and the installer prompt.

### 3. Wake Your Mac Remotely

From any device with the `wakeup` binary and config:

```bash
# Wake a specific Mac
wakeup send office

# Wake with custom duration
wakeup send office 1h

# Wake all Macs
wakeup send --all

# Check device status (online/offline/pending wake)
wakeup status

# List registered devices
wakeup devices
```

Example `wakeup status` output:

```
  office               online                     (last seen: 3m12s ago)
  home-mini            offline                    (last seen: 2h15m ago)
  home-mbp             online | PENDING WAKE      (last seen: 1m5s ago)

  2 online, 1 offline, 3 total, 1 pending wake
```

Or use curl directly:

```bash
curl -X POST https://wakeup-worker.your-subdomain.workers.dev/YOUR_TOKEN/wake/office \
  -H 'Content-Type: application/json' \
  -d '{"duration": 3600}'
```

## CLI Reference

```
wakeup daemon                       Run the wake-check daemon (foreground)
wakeup send <device_id> [duration]  Send wake signal to a device (default: 30m)
wakeup send --all [duration]        Send wake signal to all devices
wakeup status                       Show status of all devices
wakeup devices                      List registered devices
wakeup install                      Install as launchd daemon (requires sudo)
wakeup uninstall                    Uninstall launchd daemon (requires sudo)
wakeup version                      Print version
```

## Configuration

Config file location: `/etc/wakeup/config.toml` (daemon) or `~/.config/wakeup/config.toml` (CLI).

```toml
worker_url = "https://wakeup-worker.your-subdomain.workers.dev"
token = "your-auth-token"
device_id = "office"
check_interval = "15m"
default_duration = "30m"
ac_check_interval = "2m"        # check interval on AC power (v0.2.0+)
battery_check_interval = "15m"  # check interval on battery (v0.2.0+)
```

All values can be overridden with environment variables:

| Variable | Description |
|----------|-------------|
| `WAKEUP_WORKER_URL` | Cloudflare Worker URL |
| `WAKEUP_TOKEN` | Auth token |
| `WAKEUP_DEVICE_ID` | Device identifier |
| `WAKEUP_CHECK_INTERVAL` | Fallback check interval (e.g. `10m`) |
| `WAKEUP_DEFAULT_DURATION` | Default wake duration (e.g. `1h`) |
| `WAKEUP_AC_CHECK_INTERVAL` | Check interval on AC power (e.g. `2m`) |
| `WAKEUP_BATTERY_CHECK_INTERVAL` | Check interval on battery (e.g. `15m`) |

## Worker API

All endpoints require the auth token as the first path segment.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/{token}/wake/{device_id}` | Send wake signal to a device |
| `POST` | `/{token}/wake?all=true` | Send wake signal to all devices |
| `GET` | `/{token}/check/{device_id}` | Check and consume wake signal (daemon use) |
| `GET` | `/{token}/status` | Status of all devices |
| `GET` | `/{token}/devices` | List registered devices |

POST body (optional): `{"duration": 1800}` (seconds, default 1800 = 30 min)

## How the Wake Chain Works

```
Mac sleeps
  → pmset relative wake (120s on AC, 900s on battery)
  → hardware RTC wakes Mac (FullWake)
  → daemon checks Cloudflare KV
  → no signal? schedule next wake, Mac sleeps again
  → signal found? caffeinate -s -t N keeps Mac awake
  → Tailscale reconnects (~1-5 sec)
  → duration expires → caffeinate exits → Mac sleeps naturally
  → cycle continues
```

The daemon adapts its check interval based on power state — on AC power it checks every ~2 minutes (configurable), on battery every ~15 minutes. The chain is self-healing: if it breaks (crash, reboot), launchd restarts the daemon, which immediately schedules the next wake.

## Uninstall

```bash
sudo wakeup uninstall
```

This stops the daemon, removes the binary and launchd plist, clears the pmset repeat schedule, and optionally removes the config file.

## Development

```bash
# Build
make build

# Run tests
make test

# Run daemon in foreground (dev mode)
make dev

# Deploy worker
make deploy-worker
```

## Limitations

- **~2 min wake latency on AC** — the Mac checks every 2 minutes on AC power by default. On battery, the default is 15 minutes to preserve battery life. Both are configurable.
- **Not instant** — if you need sub-second wake, you need a LAN relay device with Wake-on-LAN (see [tailscale-wakeonlan](https://github.com/andygrundman/tailscale-wakeonlan)).

## License

MIT
