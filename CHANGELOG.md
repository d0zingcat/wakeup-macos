# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-04-15

### Added

- macOS 远程唤醒守护进程，通过 Cloudflare Workers 实现远程唤醒
- 使用 `pmset relative wake` 定时硬件 RTC 唤醒（默认每 15 分钟）
- 唤醒后通过 Cloudflare Worker（KV 存储）检查唤醒信号
- 收到信号后使用 `caffeinate` 保持 Mac 唤醒指定时长（默认 30 分钟）
- CLI 命令支持：`daemon`、`send`、`status`、`devices`、`install`、`uninstall`、`version`
- Cloudflare Worker 端点：wake/check/status/devices
- 设备在线/离线状态查询功能
- TOML 配置文件支持，兼容环境变量覆盖
- `launchd` plist 配置，支持系统级守护进程管理
- Makefile 构建系统（build、test、install、dev、deploy-worker、clean）
- 快速安装脚本 `scripts/install.sh`

### Documentation

- 完整的 README 文档，包含安装、配置、使用说明
- Worker 部署详细指南
- 示例配置文件 `config.example.toml` 和 `wrangler.toml.example`

[0.1.0]: https://github.com/d0zingcat/wakeup-macos/releases/tag/v0.1.0
