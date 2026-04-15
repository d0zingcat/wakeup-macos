---
title: "feat: 缩短唤醒延迟 — 自适应电源策略 + darkwake 搭便车"
type: feat
status: active
date: 2026-04-15
origin: docs/plans/2026-04-15-001-feat-wakeup-macos-daemon-plan.md
deepened: 2026-04-15
---

# feat: 缩短唤醒延迟 — 自适应电源策略 + darkwake 搭便车

## Overview

当前系统的唤醒延迟上限是 `check_interval`（默认 15 分钟）。本计划通过两阶段策略将延迟大幅缩短：

**Phase 1（核心价值）：自适应 pmset relative wake 间隔**
- 插电时将 `pmset relative wake` 间隔缩短至 2 分钟（可配），硬件 RTC 保证 FullWake，网络 100% 可用
- 电池时保持保守间隔（默认 15 分钟）
- 修复现有 `d.session` data race

**Phase 2（可选优化）：darkwake 搭便车**
- 利用系统 darkwake 事件做机会性信号检查，进一步降低实际延迟
- 通过配置开关 `enable_darkwake_detection` 控制，默认关闭
- 使用 wall clock 比较（非 monotonic clock）检测唤醒事件

Phase 1 单独即可将插电延迟从 15 分钟降至 2 分钟，覆盖用户的主要场景。Phase 2 是锦上添花，主要在电池模式下有价值。

## Problem Frame

- 当前 15 分钟的唤醒延迟对于"发信号后想尽快连上"的场景太长
- 用户的 Mac 大部分时间插电，频繁轮询对续航无影响
- macOS（尤其 Apple Silicon）在休眠时已经频繁进入 darkwake，这些唤醒事件可以搭便车做信号检查
- 需要在不增加硬件损耗的前提下尽可能缩短延迟

## Requirements Trace

- R1. 插电时唤醒延迟从 15 分钟降至 1-2 分钟（可配置）
- R2. 休眠时可选利用 darkwake 事件搭便车检查，进一步降低实际延迟
- R3. 电池模式下保持保守间隔，不显著影响续航
- R4. 向后兼容：不改变现有配置文件格式的必填字段，新字段有合理默认值
- R5. 不引入 cgo 依赖（保持纯 Go + shell out 的架构）
- R6. 修复现有 `d.session` 并发访问的 data race
- R7. shutdown 时快速退出，不因 HTTP 超时阻塞（尤其 darkwake 期间）

## Scope Boundaries

- 不实现 WebSocket/长连接推送（复杂度过高，且 darkwake 期间连接维护不可靠）
- 不实现 APNs 推送唤醒（需要 Apple 开发者账号和 iOS app）
- 不修改 Cloudflare Worker 端（纯客户端优化）

### Deferred to Separate Tasks

- Phase 2 darkwake 搭便车：可在 Phase 1 稳定后单独实现

## Context & Research

### macOS 电源管理机制

**pmset relative wake（Phase 1 核心）：**
- 使用硬件 RTC 调度唤醒，确定性高，触发 FullWake
- FullWake 期间 CPU、网络、磁盘完全可用
- 最短可靠间隔约 60-120 秒（`power.go` 已有 60 秒下限）
- 插电时每 2 分钟 FullWake 的功耗影响可忽略

**darkwake（Phase 2 优化）：**
- Apple Silicon Mac 休眠时频繁进入 darkwake（约 45-60 秒一次）
- darkwake 期间 CPU、网络可用（标志位 `[CDNPB]` 中 N=Network）
- 用户空间守护进程在 darkwake 期间正常执行

### Go monotonic clock 与 sleep 的交互（关键发现）

Go 的 `time.Ticker` 在 Darwin 上使用 `mach_absolute_time`（monotonic clock），该时钟在系统休眠期间**停止计时**。这意味着：

- `time.Since(lastTick)` 使用 monotonic 读数，**不会**显示时间跳跃
- `time.Time.Sub()` 在两个时间都有 monotonic 读数时优先使用 monotonic 比较
- 要检测休眠唤醒，**必须**使用 wall clock：`time.Now().Round(0)` 剥离 monotonic 读数后比较
- 参考 Go issue [#36141](https://github.com/golang/go/issues/36141)（`ExternalNow` 提案，截至 Go 1.26 未实现）

此外，短暂的 darkwake（2-3 秒）可能不足以让 30 秒 ticker 的剩余等待时间耗尽，因此不是每次 darkwake 都能被捕获。实际延迟预期 1-2 个 darkwake 周期（45-120 秒），而非每次都命中。

### 现有并发问题分析

当前 `daemon.go` 存在以下并发问题：

1. **`d.session` data race**：主 goroutine（`check()`）和后台 goroutine（session monitor）同时读写 `d.session`，无 mutex 保护
2. **session 指针别名**：如果 `check()` 替换了 session（停止旧的、启动新的），旧 session 的 monitor goroutine 仍会将 `d.session` 设为 nil，导致新 session 被孤立
3. **`scheduleNextWake()` 竞争**：多个代码路径调用 `scheduleNextWake()`，`pmset relative wake` 只有一个 RTC 闹钟槽位，后写入者覆盖前者

**解决方案：channel-based session 管理**
- 后台 goroutine 不直接修改 `d.session`，而是通过 `sessionDone chan *power.Session` 通知主循环
- 主循环收到通知后验证是否仍是当前 session，再决定是否清理
- 所有状态修改集中在主循环的 select 中，消除并发问题

### Relevant Code and Patterns

- `internal/daemon/daemon.go:45-63` — 当前单 ticker 主循环
- `internal/daemon/daemon.go:105-110` — session monitor goroutine（data race 源头）
- `internal/power/power.go:82-94` — `ScheduleNextWake`，最小 60 秒
- `internal/config/config.go:12-18` — Config 结构体

### External References

- Go issue [#36141](https://github.com/golang/go/issues/36141) — monotonic clock 与系统休眠
- Apple `pmset(1)` man page — `relative wake`, `-g ps`
- macOS darkwake 标志位: `[CDNPB]`

## Key Technical Decisions

- **Phase 1 优先，Phase 2 可选**：仅缩短 `pmset relative wake` 间隔即可将插电延迟降至 2 分钟，无需 darkwake 检测的复杂性。darkwake 搭便车主要在电池模式下有价值，作为可选优化单独实现。理由：用户明确表示大部分时间插电，Phase 1 已覆盖主要场景
- **channel-based session 管理而非 mutex**：后台 goroutine 通过 channel 通知主循环，而非直接修改共享状态。消除 session 指针别名问题，保持主循环单线程语义
- **wall clock 检测唤醒事件（Phase 2）**：使用 `time.Now().Round(0)` 剥离 monotonic 读数，比较 wall clock 时间。Go 的 monotonic clock 在 macOS 休眠期间停止，直接用 `time.Since()` 无法检测时间跳跃
- **`scheduleNextWake()` 调用时查询电源状态**：不在闭包中捕获间隔值，而是每次调用时根据当前电源状态选择合适的间隔，避免 session monitor goroutine 使用过时的间隔值
- **配置向后兼容**：新增 `ac_check_interval` 和 `battery_check_interval` 可选字段，原 `check_interval` 保留作为统一默认值

## Open Questions

### Resolved During Planning

- **Q: darkwake 期间网络是否可用？** → 是，`pmset -g log` 显示 darkwake 标志位包含 `N`（Network）
- **Q: Go 的 time.Ticker 能否检测休眠唤醒？** → 不能直接用 `time.Since()`，必须用 wall clock（`time.Now().Round(0)`）比较。Go 的 monotonic clock 在 macOS 休眠期间停止计时
- **Q: 是否需要双 ticker 架构？** → Phase 1 不需要。单 ticker + 自适应间隔已足够。Phase 2 可选添加快 ticker 做 darkwake 检测
- **Q: 如何避免 session monitor goroutine 的 data race？** → 使用 `sessionDone` channel 通知主循环，不直接修改 `d.session`

### Deferred to Implementation

- **Q: `pmset -g ps` 在 darkwake 期间是否能正确返回电源状态？** → 实现时测试验证
- **Q: Apple Silicon 上 `pmset relative wake` 最短可靠间隔是多少？** → 实现时测试，研究表明 60 秒是安全下限，120 秒更可靠
- **Q: darkwake 搭便车的实际命中率是多少？** → Phase 2 实现时通过日志统计

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

### Phase 1: 自适应间隔 + 并发修复

```
┌─────────────────────────────────────────────────────────────┐
│                    Daemon 主循环（Phase 1）                   │
│                                                              │
│  select {                                                    │
│  case <-checkTicker.C:                                       │
│      powerState = IsOnACPower()                              │
│      interval = ac_interval or battery_interval              │
│      scheduleNextWake(interval)                              │
│      check()                                                 │
│                                                              │
│  case sess := <-sessionDone:     ← channel, 非直接修改       │
│      if sess == currentSession:                              │
│          currentSession = nil                                │
│          scheduleNextWake(currentInterval())                 │
│                                                              │
│  case <-sigCh:                                               │
│      shutdown()                                              │
│  }                                                           │
└─────────────────────────────────────────────────────────────┘
```

### Phase 2: darkwake 搭便车（可选）

```
┌─────────────────────────────────────────────────────────────┐
│  新增 wakeTicker (30s) + wall clock 时间跳跃检测             │
│                                                              │
│  case <-wakeTicker.C:                                        │
│      wallNow = time.Now().Round(0)  ← 剥离 monotonic        │
│      elapsed = wallNow.Sub(lastWallTime)                     │
│      if elapsed > 2 * wakeDetectInterval:                    │
│          → 检测到唤醒事件                                    │
│          → if timeSinceLastCheck > wakeDetectInterval: check()           │
│      lastWallTime = wallNow                                  │
└─────────────────────────────────────────────────────────────┘
```

## Implementation Units

### Phase 1: 自适应间隔 + 并发修复

- [ ] **Unit 1: 电源状态检测**

**Goal:** 添加检测 AC/Battery 电源状态的能力

**Requirements:** R3

**Dependencies:** None

**Files:**
- Modify: `internal/power/power.go`
- Modify: `internal/power/power_test.go`

**Approach:**
- 新增 `IsOnACPower() bool` 函数，执行 `pmset -g ps` 并解析输出第一行
- 包含 `AC Power` 返回 true，否则返回 false
- 命令执行失败时默认返回 true（保守策略：失败时按插电处理，使用更短间隔）
- 为可测试性，解析逻辑抽取为纯函数 `parseACPower(output string) bool`

**Patterns to follow:**
- `internal/power/power.go` 中现有的 `exec.Command` + 输出解析模式

**Test scenarios:**
- Happy path: `parseACPower("Now drawing from 'AC Power'\n...")` → 返回 true
- Happy path: `parseACPower("Now drawing from 'Battery Power'\n...")` → 返回 false
- Edge case: 空字符串输入 → 返回 true（保守默认）
- Edge case: 非预期格式（如 UPS）→ 返回 true（保守默认）

**Verification:**
- 在插电和拔电状态下分别运行，返回值正确
- 纯函数 `parseACPower` 的单元测试覆盖所有场景

---

- [ ] **Unit 2: 配置扩展 — 自适应间隔**

**Goal:** 扩展配置结构支持插电/电池不同的检查间隔

**Requirements:** R1, R3, R4

**Dependencies:** None

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.toml`

**Approach:**
- 新增可选配置字段：
  - `ac_check_interval`（插电时检查间隔，默认 2m）
  - `battery_check_interval`（电池时检查间隔，默认 15m）
- 向后兼容逻辑：
  - 如果用户只设了 `check_interval`，则 `ac_check_interval` 和 `battery_check_interval` 都继承它
  - 如果用户显式设了 `ac_check_interval` 或 `battery_check_interval`，则覆盖 `check_interval` 对应的值
- 验证：两个新字段最小 1m
- 新增环境变量：`WAKEUP_AC_CHECK_INTERVAL`、`WAKEUP_BATTERY_CHECK_INTERVAL`

**Patterns to follow:**
- `internal/config/config.go` 中现有的默认值 + 环境变量覆盖模式

**Test scenarios:**
- Happy path: 只设 `check_interval=5m` → ac 和 battery 都是 5m
- Happy path: 设 `ac_check_interval=2m` + `battery_check_interval=10m` → 各自生效
- Happy path: 同时设 `check_interval=5m` + `ac_check_interval=2m` → ac=2m, battery=5m
- Edge case: `ac_check_interval=30s` → 验证失败（低于 1m 最小值）
- Edge case: 只设 `battery_check_interval=10m`，不设其他 → ac 使用默认 2m，battery=10m

**Verification:**
- 现有测试继续通过（向后兼容）
- 新配置字段正确解析和验证

---

- [ ] **Unit 3: 守护进程并发修复 + 自适应间隔**

**Goal:** 修复 data race，实现基于电源状态的自适应轮询间隔

**Requirements:** R1, R3, R6

**Dependencies:** Unit 1, Unit 2

**Files:**
- Modify: `internal/daemon/daemon.go`

**Approach:**

并发修复：
- 新增 `sessionDone chan *power.Session` 字段
- session monitor goroutine 改为向 `sessionDone` 发送完成的 session，不直接修改 `d.session`
- 主循环 select 新增 `case sess := <-sessionDone`，验证 `sess == d.session` 后再清理
- 所有 `d.session` 的读写集中在主循环中，消除并发访问
- 所有 `scheduleNextWake()` 调用也集中在主循环的 select 中，由于 select 是单 goroutine 串行执行，自然消除了 RTC 闹钟槽位的竞争问题

自适应间隔：
- 每次 check 前调用 `power.IsOnACPower()` 获取电源状态
- 根据电源状态选择 `cfg.ACCheckInterval` 或 `cfg.BatteryCheckInterval`
- `scheduleNextWake()` 使用当前电源状态对应的间隔
- 电源状态变化时（如拔电），下次 check 自动调整间隔
- checkTicker 使用较短的 `ac_check_interval` 作为基础间隔，在电池模式下通过 `lastCheckTime` 跳过未到间隔的 check（即 ticker 触发时，如果距上次 check 不足 `battery_check_interval`，则跳过本次 check 但仍更新 `scheduleNextWake`）

**Patterns to follow:**
- 现有 `daemon.go` 的 select 循环模式
- Go channel 通信模式（替代共享内存 + mutex）

**Test scenarios:**
- Happy path: 插电状态 → `scheduleNextWake` 使用 ac_check_interval（2m）
- Happy path: 电池状态 → `scheduleNextWake` 使用 battery_check_interval（15m）
- Happy path: session 正常结束 → 通过 sessionDone channel 通知主循环 → 清理 session → 调度下次唤醒
- Edge case: session 被替换后旧 monitor 发送通知 → 主循环检测到 `sess != d.session`，忽略
- Edge case: 电源状态切换（拔电）→ 下次 check 自动使用更长间隔
- Edge case: 电池模式下 ticker 触发但距上次 check 不足 battery_check_interval → 跳过 check，仅更新 scheduleNextWake
- Error path: `IsOnACPower()` 失败 → 默认 AC，使用短间隔
- Integration: shutdown 信号 → 停止当前 session → 进程退出

**Verification:**
- `go vet` 和 `-race` 标志下无 data race 报告
- 插电时日志显示 2 分钟间隔
- 电池时日志显示 15 分钟间隔

---

- [ ] **Unit 4: check() 添加 context 支持**

**Goal:** 为 HTTP 请求添加 context 取消支持，避免 shutdown 时长时间阻塞

**Requirements:** R7

**Dependencies:** Unit 3

**Files:**
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/cloud/client.go`

**Approach:**
- `check()` 接受 `context.Context` 参数
- context 需要贯穿 `client.Check()` → `doWithRetry()` → `do()` 整个调用链，`do()` 中 `http.NewRequest` 改为 `http.NewRequestWithContext`
- 这会影响 `doWithRetry`/`do` 的签名，进而影响所有 5 个 API 方法（`Check`、`Send`、`SendAll`、`Status`、`Devices`），但只有 `Check` 在 daemon 中使用 context 取消
- `sigCh` handler 调用 `cancel()` 统一 shutdown 路径，移除当前 `daemon.go:55` 中不可达的 `ctx.Done()` case
- 当前 HTTP 超时 10s × 3 次重试 = 最长 30s 阻塞，在 darkwake 期间这会不必要地延长唤醒时间

**Patterns to follow:**
- Go 标准 `context.Context` 传播模式
- `internal/cloud/client.go` 中现有的 `doWithRetry`/`do` 调用链模式

**Test scenarios:**
- Happy path: 正常 check 请求成功完成
- Edge case: context 被取消 → HTTP 请求立即中断，check 返回 context 错误
- Integration: shutdown 信号 → 进行中的 check 被取消 → 快速退出

**Verification:**
- shutdown 时不再等待 HTTP 超时
- 现有 cloud client 测试继续通过

---

- [ ] **Unit 5: CLI install 交互更新**

**Goal:** 安装流程支持新的配置字段

**Requirements:** R4

**Dependencies:** Unit 2

**Files:**
- Modify: `cmd/wakeup/main.go`

**Approach:**
- 在 `install` 交互流程中新增可选提示：
  - 插电检查间隔（默认 2m）
  - 电池检查间隔（默认 15m）
- 保持简洁：如果用户直接回车，使用默认值
- 写入配置文件时包含新字段
- `status` 命令输出中新增显示当前电源状态（AC/Battery）和生效的检查间隔

**Patterns to follow:**
- `cmd/wakeup/main.go` 中现有的 `prompt` 交互模式

**Test scenarios:**
- Happy path: install 时设置 ac_check_interval=1m → 配置文件正确写入
- Happy path: install 时直接回车 → 使用默认值 2m/15m
- Happy path: `wakeup status` 显示当前电源状态和对应的检查间隔
- Edge case: 输入无效值 → 提示重新输入

**Verification:**
- 安装后配置文件包含新字段
- 守护进程使用新配置正确运行

---

- [ ] **Unit 6: 文档和 CHANGELOG 更新**

**Goal:** 更新文档反映新的自适应唤醒机制

**Requirements:** None（文档质量）

**Dependencies:** Unit 3, Unit 5

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`
- Modify: `config.example.toml`

**Approach:**
- README 更新：
  - 配置参考中添加 `ac_check_interval` 和 `battery_check_interval` 说明
  - 唤醒链说明中解释自适应行为
  - 更新"最大延迟 15 分钟"的描述为"插电时 ~2 分钟，电池时 15 分钟"
- CHANGELOG 添加 v0.2.0 条目
- config.example.toml 添加新字段示例和注释

**Test expectation:** none — 纯文档

**Verification:**
- 文档准确反映新行为

### Phase 2: darkwake 搭便车（可选，单独 PR）

- [ ] **Unit 7: darkwake 检测（wall clock 时间跳跃）**

**Goal:** 添加可选的 darkwake 搭便车机制

**Requirements:** R2

**Dependencies:** Phase 1 全部完成

**Files:**
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/config/config.go`
- Modify: `config.example.toml`

**Approach:**
- 新增配置：`enable_darkwake_detection`（bool，默认 false）、`wake_detect_interval`（默认 30s，最小 10s）
- 启用时，新增快 ticker（`wake_detect_interval`）
- 时间跳跃检测**必须使用 wall clock**：
  - `lastWallTime` 存储为 `time.Now().Round(0)`（剥离 monotonic 读数）
  - 比较时同样使用 `time.Now().Round(0).Sub(lastWallTime)`
  - 不能使用 `time.Since()`，因为 Go 的 monotonic clock 在 macOS 休眠期间停止
- 检测到跳跃（elapsed > 2 × wakeDetectInterval）时，如果距上次 check 超过 `wake_detect_interval`（即 minGap = wakeDetectInterval，硬编码等于快 ticker 间隔），触发 check
- 日志记录 darkwake 检测事件，便于统计命中率

**Patterns to follow:**
- Phase 1 中建立的 channel-based 主循环模式

**Test scenarios:**
- Happy path: 模拟 wall clock 跳跃（手动设置 lastWallTime 为过去时间）→ 触发检查
- Happy path: 正常 tick（无跳跃）→ 不触发额外检查
- Edge case: 跳跃检测触发但距上次 check < minGap → 跳过
- Edge case: `enable_darkwake_detection=false` → 不创建快 ticker
- Edge case: 非常短的 darkwake（ticker 未触发）→ 无影响，等待下次

**Verification:**
- 启用后日志显示 darkwake 检测事件
- 禁用时行为与 Phase 1 完全一致
- `go test -race` 无报告

## System-Wide Impact

- **Interaction graph:** 新增 `pmset -g ps` 调用（每次 check 时），频率低（1-15 分钟一次），影响可忽略
- **Error propagation:** 电源状态检测失败时保守默认为 AC（使用短间隔），不影响核心功能。HTTP 请求支持 context 取消，shutdown 时快速退出
- **State lifecycle risks:**
  - `d.session` 并发访问通过 `sessionDone` channel 修复，消除指针别名问题
  - `pmset relative wake` 只有一个 RTC 闹钟槽位，`scheduleNextWake()` 每次调用时查询当前电源状态，避免使用过时间隔
- **API surface parity:** Worker API 不变，CLI 命令不变，仅新增配置字段
- **Unchanged invariants:** caffeinate 的使用方式不变，launchd plist 不变，Worker 端不变
- **Cloudflare Worker 请求量影响:** 插电 + 2 分钟间隔 = 每天 720 次/设备（vs 当前 96 次），仍远低于免费额度（10 万次/天）

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| `pmset relative wake` 在 60-120 秒间隔下不可靠 | 最小间隔硬编码为 60 秒，默认 2 分钟。实现时在 Apple Silicon 上测试验证 |
| 频繁 FullWake 增加功耗（电池模式） | 电池模式使用保守间隔（默认 15m），仅插电时使用短间隔 |
| Go monotonic clock 在 macOS 休眠期间停止（Phase 2） | 使用 `time.Now().Round(0)` 剥离 monotonic 读数，仅比较 wall clock |
| darkwake 期间 HTTP 请求超时延长唤醒时间 | context 取消支持（Unit 4），darkwake 检测默认关闭 |
| session monitor goroutine 与主循环的竞争条件 | channel-based 通信替代直接状态修改 |

## Sources & References

- **Origin document:** [docs/plans/2026-04-15-001-feat-wakeup-macos-daemon-plan.md](docs/plans/2026-04-15-001-feat-wakeup-macos-daemon-plan.md)
- Related code: `internal/daemon/daemon.go`, `internal/power/power.go`, `internal/config/config.go`
- Go issue [#36141](https://github.com/golang/go/issues/36141) — monotonic clock 与系统休眠
- Apple `pmset(1)` man page — `relative wake`, `-g ps`, `-g log`
- macOS darkwake 标志位: `[CDNPB]` — C=CPU, D=Disk, N=Network, P=PowerNap, B=Battery
