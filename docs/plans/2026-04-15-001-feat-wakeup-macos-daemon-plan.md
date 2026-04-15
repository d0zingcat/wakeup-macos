---
title: "feat: macOS 远程唤醒守护进程 (wakeup-macos)"
type: feat
status: active
date: 2026-04-15
---

# feat: macOS 远程唤醒守护进程 (wakeup-macos)

## Overview

构建一个 Go 守护进程 + Cloudflare Worker 云端信号服务，解决 macOS 安装 Tailscale 后休眠导致无法远程连接的问题。Mac 休眠后每 15 分钟自动唤醒检查云端信号，收到唤醒指令后保持清醒 30 分钟（可自定义），到期后恢复正常休眠。

## Problem Frame

- macOS 安装 Tailscale 后，休眠会导致 Tailscale 断开（WireGuard 基于 UDP，不受 tcpkeepalive 保护）
- 没有办公 VPN，无法使用传统 Wake-on-LAN（需要同一局域网）
- 不想完全禁用休眠（对硬件有损耗，尤其是家用电脑）
- 需要一种按需唤醒机制：发信号 → Mac 醒来 → Tailscale 重连 → 远程桌面可用 → 到期后恢复休眠

## Requirements Trace

- R1. Mac 休眠后能通过外部信号唤醒，最大延迟 15 分钟
- R2. 唤醒后保持清醒指定时长（默认 30 分钟），到期自动恢复休眠
- R3. 发送唤醒信号时可自定义清醒时长
- R4. 不需要额外硬件，纯软件方案
- R5. 守护进程开机自启，崩溃自动重启
- R6. 云端信号服务轻量、免费、低延迟
- R7. CLI 工具可从任意设备发送唤醒信号
- R8. 对 Mac 电池/硬件影响最小化
- R9. 支持多设备定向唤醒（一个 Worker 管理多台 Mac，按设备 ID 精确唤醒）

## Scope Boundaries

- 仅支持 macOS（Apple Silicon + Intel）
- 不实现 GUI / 菜单栏应用
- 不实现 Wake-on-LAN（需要额外硬件）
- 不实现认证/鉴权（依赖 Cloudflare Worker 的 URL 不可猜测性 + 可选 token）

### Deferred to Separate Tasks

- iOS Shortcut / 网页触发界面：未来迭代
- Homebrew formula 分发：未来迭代

## Context & Research

### 技术原理

**macOS 休眠与唤醒机制：**
- `pmset repeat wakeorpoweron` 可调度硬件 RTC 定时唤醒，这是完整唤醒（FullWake），网络、CPU 完全可用
- `caffeinate -s -t <seconds>` 是 macOS 自带工具，创建 IOPMAssertion 阻止系统休眠，到期自动释放
- `pmset relative wake <seconds>` 可从守护进程内部调度下一次唤醒

**Tailscale 行为：**
- 休眠时 WireGuard 隧道断开
- FullWake 后 Tailscale 自动重连，通常 1-5 秒

**唤醒调度策略：**
- `pmset repeat` 只支持每天固定时间点，不支持"每 N 分钟"间隔
- 实际方案：使用 `pmset relative wake 900`（900 秒 = 15 分钟）链式调度——每次唤醒检查完毕后，调度下一次唤醒，形成循环
- 守护进程启动时（包括开机和从休眠唤醒后）立即调度下一次唤醒
- 如果链式调度断裂（如意外重启），launchd KeepAlive 会重启守护进程，重新开始调度链

**为什么选 pmset relative wake 而非 DarkWake 拦截：**
- DarkWake 时机不可预测，网络可用性不保证
- DarkWake 期间 IOPMAssertion 行为文档不足
- `pmset relative wake` 使用硬件 RTC，确定性高，全平台兼容

### Relevant Code and Patterns

- 全新项目，无现有代码
- 参考项目：`andygrundman/tailscale-wakeonlan`（LAN relay 方案，架构参考）
- Go 库：`os/exec` 调用 `caffeinate` 和 `pmset`

### External References

- Apple QA1340: Registering for sleep/wake notifications
- `pmset(1)` man page: `repeat`, `relative wake`, `schedule wake`
- `caffeinate(8)` man page: `-s`, `-t` flags
- Cloudflare Workers KV documentation

## Key Technical Decisions

- **Go 语言**：编译为单一二进制，部署简单，适合守护进程
- **pmset relative wake 链式调度而非 DarkWake**：确定性高，不依赖 Power Nap 行为，全硬件兼容。每次检查后调度下一次唤醒
- **caffeinate CLI 而非 cgo IOKit**：避免 cgo 复杂性，caffeinate 是 macOS 原生工具，功能完全满足需求
- **Cloudflare Worker + KV**：免费额度充足（每天 10 万次读、1000 次写），全球边缘节点，延迟低
- **单二进制同时作为 daemon 和 CLI**：`wakeup daemon` 运行守护进程，`wakeup send` 发送唤醒信号，减少分发复杂度
- **URL token 认证**：Worker URL 中包含随机 token 路径段，简单有效，避免复杂鉴权
- **设备 ID 定向唤醒**：每台 Mac 配置唯一 device_id（如 `office`、`home-mini`、`home-mbp`），KV key 按设备隔离（`wake-signal:{device_id}`），CLI 发送时指定目标设备，支持 `--all` 广播唤醒

## Open Questions

### Resolved During Planning

- **Q: DarkWake 还是 pmset repeat？** → pmset repeat，确定性更高
- **Q: cgo IOKit 还是 shell out？** → shell out 到 caffeinate/pmset，简单可靠
- **Q: 如何防止未授权唤醒？** → Worker URL 包含随机 token，足够安全
- **Q: 多设备如何定向唤醒？** → 每台 Mac 配置唯一 device_id，KV key 按 `wake-signal:{device_id}` 隔离，CLI 发送时指定目标设备

### Deferred to Implementation

- **Q: pmset repeat 在 Apple Silicon 低电量时的行为？** → 实现时测试
- **Q: caffeinate 进程被意外 kill 后的恢复策略？** → 守护进程主循环处理

## Output Structure

```
wakeup-macos/
├── cmd/
│   └── wakeup/
│       └── main.go              # CLI 入口
├── internal/
│   ├── daemon/
│   │   └── daemon.go            # 守护进程核心逻辑
│   ├── cloud/
│   │   └── client.go            # Cloudflare Worker API 客户端
│   ├── power/
│   │   └── power.go             # caffeinate/pmset 封装
│   └── config/
│       └── config.go            # 配置管理
├── worker/
│   ├── src/
│   │   └── index.ts             # Cloudflare Worker 代码
│   ├── wrangler.toml             # Worker 配置
│   └── package.json
├── scripts/
│   └── install.sh               # 安装脚本（安装二进制 + launchd plist）
├── com.wakeup.daemon.plist       # launchd 配置
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```
┌─────────────────────────────────────────────────────────┐
│                    发送端 (任意设备)                       │
│  wakeup send office --duration 30m                      │
│  wakeup send --all                                      │
│  → POST /{token}/wake/{device_id}                       │
└──────────────────────────┬──────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────┐
│              Cloudflare Worker + KV                       │
│  POST /wake/{device_id} → KV key: wake-signal:{id}      │
│  POST /wake?all=true    → KV key: wake-signal:*          │
│  GET  /check/{device_id} → 读取该设备信号，读后清除      │
│  GET  /status            → 返回所有设备状态               │
│  GET  /devices           → 列出已注册设备                 │
└──────────────────────────┬──────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────┐
│         Mac 守护进程 (wakeup daemon, device=office)      │
│                                                          │
│  ┌─ pmset relative wake: 每 15 分钟唤醒 ─┐              │
│  │                                         │             │
│  │  1. Mac 唤醒 (FullWake)                 │             │
│  │  2. GET /check/office                   │             │
│  │  3a. 有信号 → caffeinate -s -t N        │             │
│  │      → Tailscale 自动重连               │             │
│  │      → N 秒后释放，恢复休眠             │             │
│  │  3b. 无信号 → 不做任何事                │             │
│  │      → Mac 自然回到休眠                 │             │
│  └─────────────────────────────────────────┘             │
└─────────────────────────────────────────────────────────┘
```

## Implementation Units

- [ ] **Unit 1: Cloudflare Worker 信号服务**

**Goal:** 实现云端信号存储和 API（支持多设备）

**Requirements:** R6, R7, R9

**Dependencies:** None

**Files:**
- Create: `worker/src/index.ts`
- Create: `worker/wrangler.toml`
- Create: `worker/package.json`

**Approach:**
- 端点设计：
  - `POST /{token}/wake/{device_id}` — 唤醒指定设备
  - `POST /{token}/wake?all=true` — 唤醒所有已注册设备
  - `GET /{token}/check/{device_id}` — 读取并清除该设备的信号（守护进程调用）
  - `GET /{token}/status` — 查看所有设备状态
  - `GET /{token}/devices` — 列出已注册设备
- KV 存储结构：
  - 信号 key: `wake-signal:{device_id}`，value = JSON `{wake: true, duration: 1800, created_at: timestamp}`
  - 设备注册 key: `device:{device_id}`，value = JSON `{last_seen: timestamp}`（守护进程每次 check 时更新）
- 信号自动过期：写入时设置 KV TTL 为 15 分钟
- `--all` 广播：遍历所有 `device:*` key，为每个设备写入信号
- token 作为 URL 路径段，不匹配则返回 404

**Patterns to follow:**
- Cloudflare Workers KV 标准用法

**Test scenarios:**
- Happy path: POST /wake/office 写入信号，GET /check/office 读取并返回，GET /check/home-mini 返回空
- Happy path: POST /wake?all=true 为所有已注册设备写入信号
- Happy path: POST /wake/office 带自定义 duration，GET /check/office 返回对应 duration
- Happy path: GET /devices 返回所有已注册设备列表
- Edge case: 信号过期后 GET /check/{id} 返回空
- Error path: 错误 token 返回 404
- Error path: 无效 HTTP method 返回 405

**Verification:**
- `wrangler dev` 本地测试三个端点行为正确
- 信号写入后可读取，读取后自动清除

---

- [ ] **Unit 2: Go 项目骨架 + 配置管理**

**Goal:** 搭建 Go 项目结构，实现配置加载

**Requirements:** R5

**Dependencies:** None

**Files:**
- Create: `go.mod`
- Create: `cmd/wakeup/main.go`
- Create: `internal/config/config.go`

**Approach:**
- 使用 cobra 或简单的 subcommand 分发（`daemon`、`send`、`status`、`install`、`uninstall`）
- 配置文件路径：`~/.config/wakeup/config.toml` 或 `/etc/wakeup/config.toml`（daemon 模式）
- 配置项：worker_url、token、device_id（如 `office`）、check_interval（默认 15m）、default_duration（默认 30m）
- 支持环境变量覆盖

**Patterns to follow:**
- Go 标准项目布局

**Test scenarios:**
- Happy path: 加载配置文件，所有字段正确解析
- Happy path: 环境变量覆盖配置文件值
- Edge case: 配置文件不存在时使用默认值
- Error path: 配置文件格式错误时报清晰错误

**Verification:**
- `go build ./cmd/wakeup` 编译成功
- 子命令路由正确

---

- [ ] **Unit 3: 云端 API 客户端**

**Goal:** 实现与 Cloudflare Worker 通信的 HTTP 客户端（支持多设备）

**Requirements:** R1, R3, R7, R9

**Dependencies:** Unit 2

**Files:**
- Create: `internal/cloud/client.go`
- Test: `internal/cloud/client_test.go`

**Approach:**
- `Check(deviceID string)` → GET /{token}/check/{deviceID}，返回 `(signal *WakeSignal, err error)`
- `Send(deviceID string, duration time.Duration)` → POST /{token}/wake/{deviceID}
- `SendAll(duration time.Duration)` → POST /{token}/wake?all=true
- `Status()` → GET /{token}/status，查看所有设备状态
- `Devices()` → GET /{token}/devices，列出已注册设备
- HTTP 超时 10 秒，重试 2 次（间隔 2 秒）
- 网络不可用时静默失败（守护进程场景）

**Patterns to follow:**
- Go 标准 `net/http` 客户端

**Test scenarios:**
- Happy path: Check("office") 返回有效信号
- Happy path: Check("office") 返回空（无信号）
- Happy path: Send("office", 30m) 成功写入信号
- Happy path: SendAll(30m) 成功广播信号
- Happy path: Devices() 返回设备列表
- Error path: 网络超时后重试，最终返回错误
- Error path: Worker 返回 404（token 错误）时报清晰错误

**Verification:**
- 单元测试通过（使用 httptest mock server）

---

- [ ] **Unit 4: 电源管理封装 (caffeinate + pmset)**

**Goal:** 封装 macOS 电源管理命令

**Requirements:** R1, R2, R8

**Dependencies:** Unit 2

**Files:**
- Create: `internal/power/power.go`
- Test: `internal/power/power_test.go`

**Approach:**
- `KeepAwake(duration time.Duration) (*Session, error)` → 启动 `caffeinate -s -t <seconds>` 子进程，返回 session 用于提前终止
- `Session.Stop()` → kill caffeinate 进程
- `ScheduleWake(after time.Duration) error` → 执行 `pmset relative wake <seconds>`（需要 sudo）
- `SetupRepeatWake(interval time.Duration) error` → 配置 `pmset repeat wakeorpoweron` 作为 fallback（安装时调用）
- `ScheduleNextWake(after time.Duration) error` → 执行 `pmset relative wake <seconds>`（核心调度机制）
- `ClearRepeatWake() error` → 清除 pmset repeat 配置（卸载时调用）

**Patterns to follow:**
- Go `os/exec` 标准用法

**Test scenarios:**
- Happy path: KeepAwake 启动 caffeinate 进程，到期后进程退出
- Happy path: Session.Stop() 提前终止 caffeinate
- Edge case: caffeinate 进程被外部 kill 后 Session.Stop() 不 panic
- Error path: pmset 命令失败时返回清晰错误

**Verification:**
- `caffeinate` 进程正确启动和终止
- `pmset` 命令正确执行

---

- [ ] **Unit 5: 守护进程核心逻辑**

**Goal:** 实现守护进程主循环：唤醒 → 检查信号 → 保持清醒或回到休眠

**Requirements:** R1, R2, R5

**Dependencies:** Unit 3, Unit 4

**Files:**
- Create: `internal/daemon/daemon.go`
- Test: `internal/daemon/daemon_test.go`

**Approach:**
- 主循环：每 check_interval 检查一次云端信号（使用自身 device_id）
- 收到信号：启动 caffeinate session，持续 duration 时长
- 无信号：什么都不做，等待下次检查
- 已在清醒状态时收到新信号：延长清醒时间（重启 caffeinate）
- 优雅退出：收到 SIGTERM/SIGINT 时释放 caffeinate session
- 日志输出到 stdout（launchd 会捕获到系统日志）

**Patterns to follow:**
- Go context + signal handling 标准模式

**Test scenarios:**
- Happy path: 检查到信号 → 启动 caffeinate → duration 后释放
- Happy path: 无信号 → 不启动 caffeinate
- Happy path: 清醒期间收到新信号 → 延长清醒时间
- Edge case: 网络不可用时静默跳过，等待下次检查
- Error path: caffeinate 意外退出 → 日志记录，下次检查时重新启动
- Integration: SIGTERM → 释放 caffeinate → 进程退出

**Verification:**
- 守护进程正确响应信号并管理 caffeinate 生命周期
- 日志输出清晰可读

---

- [ ] **Unit 6: CLI 命令实现 (send / status)**

**Goal:** 实现从任意设备发送唤醒信号的 CLI 命令

**Requirements:** R3, R7

**Dependencies:** Unit 3

**Files:**
- Modify: `cmd/wakeup/main.go`

**Approach:**
- `wakeup send <device_id>` → 唤醒指定设备，默认 30 分钟
- `wakeup send <device_id> --duration 1h` → 自定义清醒时长
- `wakeup send --all` → 唤醒所有设备
- `wakeup status` → 查看所有设备状态
- `wakeup devices` → 列出已注册设备
- 输出简洁：成功时一行确认，失败时错误信息 + 退出码

**Patterns to follow:**
- Go CLI 标准模式

**Test scenarios:**
- Happy path: `wakeup send office` 成功发送，输出确认信息
- Happy path: `wakeup send office --duration 2h` 发送自定义时长
- Happy path: `wakeup send --all` 广播唤醒所有设备
- Happy path: `wakeup devices` 列出已注册设备
- Happy path: `wakeup status` 显示所有设备状态
- Error path: 未指定 device_id 且未使用 --all 时提示用法
- Error path: 网络不可用时报错并退出码非零

**Verification:**
- CLI 命令正确调用云端 API
- 输出信息清晰

---

- [ ] **Unit 7: 安装/卸载 + launchd 配置**

**Goal:** 实现一键安装和卸载

**Requirements:** R5

**Dependencies:** Unit 5

**Files:**
- Create: `com.wakeup.daemon.plist`
- Create: `scripts/install.sh`
- Modify: `cmd/wakeup/main.go`（添加 install/uninstall 子命令）

**Approach:**
- `wakeup install` → 复制二进制到 `/usr/local/bin/`，安装 plist 到 `/Library/LaunchDaemons/`，配置 pmset repeat，加载 daemon
- `wakeup uninstall` → 卸载 daemon，清除 pmset repeat，删除 plist 和二进制
- launchd plist: `KeepAlive: true`、`RunAtLoad: true`、stdout/stderr 重定向到 `/var/log/wakeup.log`
- 安装需要 sudo 权限

**Patterns to follow:**
- macOS launchd daemon 标准安装流程

**Test scenarios:**
- Happy path: install 后 daemon 自动启动，`launchctl list | grep wakeup` 可见
- Happy path: uninstall 后 daemon 停止，plist 和二进制被清除
- Edge case: 重复 install 不报错（先 uninstall 再 install）
- Error path: 非 sudo 执行时提示需要权限

**Verification:**
- 安装后 `ps aux | grep wakeup` 可见守护进程
- 重启后守护进程自动启动
- 卸载后系统干净

---

- [ ] **Unit 8: Makefile + 构建流程**

**Goal:** 提供便捷的构建和开发命令

**Requirements:** None (开发体验)

**Dependencies:** Unit 2

**Files:**
- Create: `Makefile`

**Approach:**
- `make build` → 编译 Go 二进制
- `make install` → 构建 + 安装
- `make uninstall` → 卸载
- `make deploy-worker` → 部署 Cloudflare Worker
- `make dev` → 本地开发模式（前台运行 daemon）

**Test expectation:** none — 纯构建配置

**Verification:**
- `make build` 产出可执行二进制

## System-Wide Impact

- **Interaction graph:** 守护进程 → caffeinate 子进程 → macOS 电源管理；守护进程 → HTTPS → Cloudflare Worker → KV
- **Error propagation:** 网络失败静默跳过（不影响休眠行为）；caffeinate 失败记录日志，下次检查重试
- **State lifecycle risks:** caffeinate 进程泄漏（守护进程崩溃时）→ launchd KeepAlive 重启守护进程，新实例检查并清理孤儿 caffeinate 进程
- **API surface parity:** CLI send 命令和直接 curl Worker API 效果相同
- **Unchanged invariants:** 不修改 macOS 系统休眠设置（除 pmset repeat），不影响 Tailscale 配置

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| pmset repeat 在某些 macOS 版本行为不一致 | 实现时在 Sonoma/Sequoia 上测试，fallback 到 pmset schedule |
| caffeinate 进程泄漏 | 守护进程启动时检查并清理孤儿进程 |
| Cloudflare Workers 免费额度用尽 | 3 台设备每 15 分钟各一次请求 = 每天 288 次，远低于免费额度（10 万次/天） |
| Mac 长时间不休眠时守护进程空转 | 检查逻辑极轻量（一次 HTTP GET），影响可忽略 |

## Sources & References

- Apple QA1340: Registering for sleep/wake notifications
- `pmset(1)` man page
- `caffeinate(8)` man page
- Cloudflare Workers KV documentation
- `andygrundman/tailscale-wakeonlan` (架构参考)
- Tailscale macOS documentation
