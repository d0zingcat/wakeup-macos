---
title: "feat: 集中式配置分发 — 通过 Worker 管理和下发设备配置"
type: feat
status: active
date: 2026-04-16
---

# feat: 集中式配置分发 — 通过 Worker 管理和下发设备配置

## Overview

当前每台 Mac 安装 wakeup daemon 时需要手动配置所有参数（check interval、duration、darkwake 等）。当设备数量增多或需要统一调整参数时，逐台修改非常繁琐。

本计划在 Cloudflare Worker 中增加配置存储能力，支持：
- 通过 CLI 推送全局配置和设备级配置到 Worker
- daemon 在每次 `/check` 心跳时拉取远程配置
- 远程配置覆盖本地配置（`worker_url`、`token`、`device_id` 除外）
- 本地安装只需最小三要素：`worker_url` + `token` + `device_id`

## Problem Frame

- 用户管理多台 Mac（家里、办公室、远程服务器），每台都需要手动编辑 config.toml
- 想统一调整 `ac_check_interval` 从 2m 改为 1m，需要 SSH 到每台机器修改
- 新设备加入时需要知道所有配置参数的推荐值
- 没有中心化的配置管理，无法做到"改一处，全生效"

## Requirements Trace

- R1. 支持通过 CLI 推送全局配置到 Worker（所有设备共享的默认值）
- R2. 支持通过 CLI 推送设备级配置到 Worker（覆盖全局配置的特定设备值）
- R3. daemon 在 `/check` 心跳时自动拉取远程配置，无需重启
- R4. 远程配置与本地配置合并：远程优先，但 `worker_url`、`token`、`device_id` 始终使用本地值
- R5. 配置变更后 daemon 热重载（调整 ticker 间隔、darkwake 开关等），无需重启进程
- R6. 向后兼容：没有远程配置时行为与现在完全一致
- R7. CLI 可查看当前生效的配置（本地 + 远程合并后的结果）
- R8. 安装流程简化：最小配置只需 `worker_url` + `token` + `device_id`

## Scope Boundaries

- 不实现配置版本历史或回滚（KV 无原生版本支持，复杂度不值得）
- 不实现配置变更通知推送（依赖现有的轮询机制即可）
- 不加密配置内容（已有 token 认证保护传输通道）
- 不实现配置模板或继承链（全局 + 设备级两层足够）

### Deferred to Separate Tasks

- Web UI 配置管理界面：未来迭代
- 配置变更审计日志：未来迭代

## Context & Research

### Relevant Code and Patterns

- `worker/src/index.ts` — 现有 Worker 路由模式：`/{token}/action/{device_id}`，KV 键前缀模式（`device:`, `wake-signal:`）
- `internal/cloud/client.go` — Go HTTP 客户端模式：`doWithRetry` 3 次重试，`NewRequestWithContext` 上下文取消
- `internal/config/config.go` — 配置加载：TOML 文件 → 环境变量覆盖 → 间隔继承 → 验证
- `internal/daemon/daemon.go` — daemon 主循环：`select` 多路复用 ticker、channel、signal
- `cmd/wakeup/main.go` — CLI 子命令模式：`switch os.Args[1]`，交互式安装向导

### Relevant Patterns

- KV 键前缀模式已建立：`device:{id}`, `wake-signal:{id}`，新增 `config:global`, `config:device:{id}` 自然延续
- Go 客户端每个 Worker 端点对应一个方法（Check, Send, Status, Devices），新增 GetConfig/PushConfig 同理
- 配置验证逻辑集中在 `config.Validate()`，远程配置合并后复用同一验证

### External References

- Cloudflare KV: 单个值最大 25MB，读取延迟 < 60ms，最终一致性（写入后全球传播约 60s）
- KV `list()` 只返回键名，需要逐个 `get()` 取值 — 但配置只有 2 个键（global + device），不是问题

## Key Technical Decisions

- **配置合并优先级：本地文件 < 远程全局 < 远程设备级 < 环境变量**：环境变量保持最高优先级（调试和临时覆盖），远程设备级覆盖全局，远程覆盖本地文件。`worker_url`、`token`、`device_id` 不参与远程覆盖（引导问题）
- **配置随 `/check` 响应一起返回，不新增端点**：daemon 已经每 2 分钟调用 `/check`，在响应中附带配置数据避免额外请求。用 `config_version` 哈希做条件获取，无变化时不传输配置体
- **KV 键设计：`config:global` + `config:device:{id}`**：延续现有前缀模式，两层结构简单清晰
- **daemon 热重载通过重建 ticker 实现**：检测到配置变更后重建 `time.Ticker`，不需要重启进程。darkwake 开关变更也通过重建 ticker channel 处理
- **新增独立 `/config` 端点用于 CLI 管理**：推送和查看配置通过 `PUT/GET /{token}/config` 和 `PUT/GET /{token}/config/{device_id}` 操作，与 `/check` 的配置下发分离

## Open Questions

### Resolved During Planning

- **Q: 远程配置格式用 TOML 还是 JSON？** → JSON。Worker 端天然处理 JSON，Go 端 `encoding/json` 零依赖。配置字段少，JSON 可读性足够
- **Q: 配置变更检测用时间戳还是哈希？** → 哈希（MD5 of JSON）。时间戳有时钟偏移问题，哈希精确且计算成本低
- **Q: `/check` 响应是否总是包含配置？** → 不是。daemon 发送本地 `config_version` 哈希，Worker 比较后仅在不同时返回配置体。减少带宽和解析开销

### Deferred to Implementation

- daemon 热重载时是否需要 drain 当前 caffeinate session — 取决于哪些配置字段变更，实现时决定
- `config:global` 和 `config:device:{id}` 的 KV TTL 策略 — 配置应持久存储，但需要确认 KV 无 TTL 时的行为

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```
配置推送流程:
  CLI (wakeup config push)
    → PUT /{token}/config         [全局配置]
    → PUT /{token}/config/{id}    [设备级配置]
    → Worker 写入 KV: config:global / config:device:{id}
    → Worker 计算并存储 config_version 哈希

配置拉取流程 (每次 /check 心跳):
  daemon
    → GET /{token}/check/{id}?cv={local_config_version}
    → Worker 合并 config:global + config:device:{id}
    → 比较哈希: 相同 → 返回 {wake: false}（无配置体）
                不同 → 返回 {wake: false, config: {...}, config_version: "..."}
    → daemon 收到新配置 → 合并到运行时 → 重建 ticker

配置合并优先级 (低 → 高):
  硬编码默认值 → 本地 TOML 文件 → 远程全局配置 → 远程设备级配置 → 环境变量
  (worker_url, token, device_id 始终取本地值，不参与远程覆盖)
```

## Implementation Units

- [ ] **Unit 1: Worker 配置存储端点**

**Goal:** 在 Worker 中新增配置的 CRUD 端点，支持全局配置和设备级配置的读写

**Requirements:** R1, R2

**Dependencies:** None

**Files:**
- Modify: `worker/src/index.ts`

**Approach:**
- 新增 `ConfigData` 接口，字段对应可远程管理的配置项（排除 worker_url/token/device_id）
- 新增 `ConfigEnvelope` 接口包含 `config` + `version`（MD5 哈希）
- `PUT /{token}/config` — 写入 `config:global`，计算并存储版本哈希到 `config-version:global`
- `GET /{token}/config` — 读取全局配置 + 版本
- `PUT /{token}/config/{device_id}` — 写入 `config:device:{id}`，计算版本哈希到 `config-version:device:{id}`
- `GET /{token}/config/{device_id}` — 读取设备级配置 + 版本
- `DELETE /{token}/config/{device_id}` — 删除设备级配置（恢复使用全局配置）
- 配置值验证：interval 类字段 >= 60s，duration >= 60s
- 版本哈希计算：对合并后的 JSON 做 MD5（使用 Web Crypto API 的 `crypto.subtle.digest`）

**Patterns to follow:**
- 现有路由模式：`action === "config" && request.method === "PUT"` 分支
- 现有 KV 操作模式：`env.WAKEUP_KV.put(key, JSON.stringify(data))`
- 现有错误响应模式：`errorResponse("message", status)`

**Test scenarios:**
- Happy path: PUT 全局配置 → GET 返回相同配置和版本哈希
- Happy path: PUT 设备级配置 → GET 返回设备配置和版本哈希
- Happy path: DELETE 设备级配置 → GET 返回 404
- Edge case: PUT 空 JSON body → 返回 400
- Edge case: PUT 包含 worker_url/token/device_id 字段 → 字段被忽略（不存储）
- Error path: PUT interval < 60s → 返回 400 验证错误
- Error path: GET 不存在的设备配置 → 返回 404

**Verification:**
- 所有配置 CRUD 端点正常工作
- 版本哈希在配置内容相同时稳定，内容变化时改变

---

- [ ] **Unit 2: Worker `/check` 响应附带配置**

**Goal:** 修改 `/check` 端点，支持条件性地在响应中附带合并后的远程配置

**Requirements:** R3, R6

**Dependencies:** Unit 1

**Files:**
- Modify: `worker/src/index.ts`

**Approach:**
- `/check/{device_id}` 接受可选 query parameter `cv`（config version）
- Worker 读取 `config:global` 和 `config:device:{device_id}`，合并（设备级覆盖全局）
- 计算合并后配置的版本哈希
- 如果 `cv` 参数缺失或与当前哈希不同，在响应中附加 `config` 和 `config_version` 字段
- 如果 `cv` 与当前哈希相同，不附加配置（节省带宽）
- 如果没有任何远程配置存在，不附加配置字段（向后兼容）
- 抽取配置合并和哈希计算为独立函数，供 Unit 1 的 GET 端点和此处复用

**Patterns to follow:**
- 现有 `/check` 响应结构：`{ wake: boolean, duration?: number, created_at?: number }`
- 新增可选字段：`{ ..., config?: ConfigData, config_version?: string }`

**Test scenarios:**
- Happy path: 有远程配置 + 无 cv 参数 → 响应包含 config 和 config_version
- Happy path: 有远程配置 + cv 匹配 → 响应不包含 config
- Happy path: 有远程配置 + cv 不匹配 → 响应包含新 config
- Happy path: 全局 + 设备级配置都存在 → 设备级字段覆盖全局字段
- Edge case: 无任何远程配置 → 响应与现有格式完全一致（向后兼容）
- Edge case: 只有全局配置无设备级 → 返回全局配置
- Integration: wake signal 存在 + 配置变更 → 两者都在响应中返回

**Verification:**
- 现有 `/check` 行为不受影响（无远程配置时）
- 配置条件获取正常工作（cv 哈希比较）
- 全局 + 设备级配置正确合并

---

- [ ] **Unit 3: Go 客户端配置方法**

**Goal:** 在 Go cloud client 中新增配置相关的 API 方法

**Requirements:** R1, R2, R7

**Dependencies:** Unit 1

**Files:**
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/client_test.go`

**Approach:**
- 新增 `RemoteConfig` 结构体，字段对应可远程管理的配置项（JSON tag）
- 新增 `ConfigResponse` 结构体：`{ Config RemoteConfig, Version string }`
- 新增方法：
  - `PushGlobalConfig(ctx, config)` — PUT `/config`
  - `PushDeviceConfig(ctx, deviceID, config)` — PUT `/config/{id}`
  - `GetGlobalConfig(ctx)` — GET `/config`
  - `GetDeviceConfig(ctx, deviceID)` — GET `/config/{id}`
  - `DeleteDeviceConfig(ctx, deviceID)` — DELETE `/config/{id}`
- 修改 `Check` 方法：接受可选 `configVersion` 参数，附加 `?cv=` query parameter
- 修改 `WakeSignal` 结构体：新增可选 `Config *RemoteConfig` 和 `ConfigVersion string` 字段

**Patterns to follow:**
- 现有方法模式：`func (c *Client) Method(ctx, ...) (result, error)` + `doWithRetry`
- 现有测试模式：`httptest.NewServer` mock + 验证请求路径和方法

**Test scenarios:**
- Happy path: PushGlobalConfig 发送正确的 PUT 请求和 JSON body
- Happy path: GetGlobalConfig 解析响应中的配置和版本
- Happy path: PushDeviceConfig 发送到正确的设备路径
- Happy path: DeleteDeviceConfig 发送 DELETE 请求
- Happy path: Check 带 configVersion 参数时附加 cv query parameter
- Happy path: Check 响应包含 config 字段时正确解析
- Edge case: Check 响应无 config 字段时 Config 为 nil（向后兼容）
- Error path: Push 请求返回 400 → 返回错误
- Error path: Get 请求返回 404 → 返回特定错误（配置不存在）

**Verification:**
- 所有新方法通过 httptest mock 测试
- Check 方法向后兼容（不传 configVersion 时行为不变）

---

- [ ] **Unit 4: 配置合并逻辑**

**Goal:** 实现本地配置与远程配置的合并逻辑，确保优先级正确

**Requirements:** R4, R6

**Dependencies:** Unit 3

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/merge.go`
- Create: `internal/config/merge_test.go`

**Approach:**
- 新增 `RemoteConfig` 结构体（与 cloud 包的对应，但属于 config 包，避免循环依赖）
- 新增 `MergeRemote(base *Config, remote *RemoteConfig) *Config` 函数
- 合并规则：遍历 remote 的每个字段，非零值覆盖 base 对应字段
- `worker_url`、`token`、`device_id` 不在 RemoteConfig 中定义，天然不会被覆盖
- 合并后调用 `Validate()` 确保结果合法
- 合并后调用 `resolveIntervals()` 确保间隔继承逻辑生效

**Patterns to follow:**
- 现有 `resolveIntervals()` 模式：字段级比较和继承
- 现有 `Validate()` 模式：集中验证

**Test scenarios:**
- Happy path: 远程配置覆盖本地 check_interval → 合并结果使用远程值
- Happy path: 远程配置只设置部分字段 → 未设置字段保留本地值
- Happy path: 远程配置设置 check_interval 但不设置 ac/battery → 间隔继承生效
- Edge case: 远程配置为 nil → 返回原始本地配置（不变）
- Edge case: 远程配置所有字段为零值 → 返回原始本地配置（零值不覆盖）
- Error path: 远程配置导致验证失败（如 interval < 1m）→ 返回错误，保留原配置
- Integration: 完整合并链：默认值 → 本地文件 → 远程全局 → 远程设备级 → 验证通过

**Verification:**
- 合并优先级正确：本地 < 远程全局 < 远程设备级
- 受保护字段（worker_url, token, device_id）不可被远程覆盖
- 验证失败时不应用远程配置

---

- [ ] **Unit 5: daemon 配置热重载**

**Goal:** daemon 在收到新远程配置后热重载运行时参数，无需重启进程

**Requirements:** R3, R5

**Dependencies:** Unit 2, Unit 4

**Files:**
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/daemon/daemon_test.go`

**Approach:**
- daemon 结构体新增 `configVersion string` 字段，记录当前远程配置版本
- 修改 `check()` 方法：调用 `client.Check()` 时传入 `configVersion`
- 收到新配置时：调用 `MergeRemote` 合并 → 验证 → 更新 `d.cfg` → 更新 `d.configVersion`
- 配置变更后的热重载：
  - `ac_check_interval` / `battery_check_interval` 变更 → 重建主 ticker（`ticker.Reset(newInterval)`）
  - `enable_darkwake_detection` 变更 → 启动或停止 darkwake ticker
  - `default_duration` 变更 → 下次 wake session 生效（不影响当前 session）
  - `wake_detect_interval` 变更 → 重建 darkwake ticker
- 日志记录配置变更：`log.Printf("config updated from remote (version: %s)", newVersion)`

**Patterns to follow:**
- 现有 ticker 管理模式：`time.NewTicker(interval)`
- 现有 darkwake ticker 条件创建模式：`if cfg.EnableDarkwakeDetection { ... }`
- 现有日志模式：`log.Printf`

**Test scenarios:**
- Happy path: check 返回新配置 → daemon 更新运行时配置和 configVersion
- Happy path: ac_check_interval 变更 → ticker 间隔更新
- Happy path: enable_darkwake_detection 从 false 变为 true → darkwake ticker 启动
- Happy path: enable_darkwake_detection 从 true 变为 false → darkwake ticker 停止
- Edge case: check 返回相同 configVersion → 不触发重载
- Edge case: check 返回无 config 字段 → 不触发重载（向后兼容）
- Error path: 远程配置验证失败 → 保留当前配置，日志记录错误
- Integration: 配置变更 + wake signal 同时存在 → 两者都正确处理

**Verification:**
- 配置变更后 ticker 间隔实际改变
- 无效远程配置不会破坏 daemon 运行
- 向后兼容：无远程配置时行为与现在完全一致

---

- [ ] **Unit 6: CLI 配置管理命令**

**Goal:** 新增 CLI 子命令用于推送、查看和管理远程配置

**Requirements:** R1, R2, R7

**Dependencies:** Unit 3

**Files:**
- Modify: `cmd/wakeup/main.go`

**Approach:**
- 新增子命令 `wakeup config` 及其子操作：
  - `wakeup config push` — 推送当前本地配置为全局配置（排除 worker_url/token/device_id）
  - `wakeup config push --device <id>` — 推送为设备级配置
  - `wakeup config get` — 查看远程全局配置
  - `wakeup config get --device <id>` — 查看远程设备级配置
  - `wakeup config delete --device <id>` — 删除设备级配置
  - `wakeup config show` — 显示当前生效的合并配置（本地 + 远程）
- `push` 从本地 config.toml 读取，提取可远程管理的字段，推送到 Worker
- `show` 加载本地配置，拉取远程配置，合并后显示，标注每个字段的来源（local/remote-global/remote-device）
- 更新 `usage()` 函数和帮助文本

**Patterns to follow:**
- 现有子命令模式：`switch os.Args[1] { case "config": runConfig() }`
- 现有 CLI 输出模式：`fmt.Printf` 格式化输出

**Test scenarios:**
- Happy path: `config push` 读取本地配置并推送全局配置
- Happy path: `config push --device mac-mini` 推送设备级配置
- Happy path: `config get` 显示远程全局配置
- Happy path: `config delete --device mac-mini` 删除设备级配置
- Happy path: `config show` 显示合并后的配置及字段来源
- Edge case: 无远程配置时 `config show` 只显示本地配置
- Error path: 无本地配置文件时 `config push` → 报错提示

**Verification:**
- 所有子命令正常工作
- `config show` 正确标注字段来源
- 帮助文本更新

---

- [ ] **Unit 7: 安装流程简化**

**Goal:** 简化安装向导，支持最小配置模式

**Requirements:** R8

**Dependencies:** Unit 5, Unit 6

**Files:**
- Modify: `cmd/wakeup/main.go`
- Modify: `config.example.toml`

**Approach:**
- 安装向导新增提示："是否使用远程配置？(y/n)"
  - 选 y：只询问 worker_url、token、device_id，其余参数从远程获取
  - 选 n：保持现有完整交互流程
- 最小配置模式写入的 config.toml 只包含三个字段
- 更新 `config.example.toml` 添加远程配置说明注释
- daemon 启动时如果本地只有最小配置，首次 check 会自动拉取远程配置

**Patterns to follow:**
- 现有交互式提示模式：`fmt.Print("prompt: ")` + `scanner.Scan()`
- 现有配置写入模式：`fmt.Sprintf` 模板

**Test scenarios:**
- Happy path: 选择远程配置模式 → 只写入 worker_url/token/device_id
- Happy path: 选择完整配置模式 → 行为与现有一致
- Edge case: 远程配置模式但 Worker 无配置 → daemon 使用硬编码默认值正常运行

**Verification:**
- 最小配置安装后 daemon 能正常启动
- 完整配置安装流程不受影响

## System-Wide Impact

- **Interaction graph:** `/check` 端点响应结构扩展（新增可选 config/config_version 字段），所有调用 Check 的代码路径受影响（daemon check loop）。新增 `/config` 端点族，CLI 新增 `config` 子命令
- **Error propagation:** 远程配置获取失败不应阻塞 daemon 运行 — 失败时保留当前配置继续工作。配置验证失败同理，日志记录但不中断
- **State lifecycle risks:** 配置热重载时 ticker 重建需要注意并发安全（在 select loop 内同步处理）。caffeinate session 不受配置变更影响（当前 session 继续，新配置下次 session 生效）
- **API surface parity:** `/check` 响应扩展向后兼容（新字段可选）。旧版 daemon 不发送 `cv` 参数，Worker 不返回配置，行为不变
- **Unchanged invariants:** wake signal 机制完全不变。token 认证机制不变。launchd plist 和 pmset 调度不变。设备注册（device: KV 键）机制不变

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| KV 最终一致性导致配置推送后短暂不一致 | 可接受 — KV 全球传播约 60s，配置变更不是实时敏感操作 |
| 远程配置损坏导致 daemon 异常 | 合并后必须通过 Validate()，验证失败则保留当前配置 |
| 热重载 ticker 时的并发问题 | 在 daemon select loop 内同步处理，不引入额外 goroutine |
| `/check` 响应变大增加延迟 | 条件获取（cv 哈希比较）确保大部分请求不携带配置体 |
| 旧版 daemon 与新版 Worker 不兼容 | 向后兼容设计：新字段可选，旧 daemon 忽略未知字段 |

## Sources & References

- Related code: `worker/src/index.ts`, `internal/cloud/client.go`, `internal/config/config.go`, `internal/daemon/daemon.go`, `cmd/wakeup/main.go`
- Related plans: `docs/plans/2026-04-15-001-feat-wakeup-macos-daemon-plan.md`, `docs/plans/2026-04-15-002-feat-reduce-wake-latency-plan.md`
- Cloudflare KV docs: limits (25MB/value, 60s eventual consistency), list/get API
