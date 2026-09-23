---
name: sub2api-codex-setup
description: "当用户需要一键安装 Codex 客户端并把 Codex CLI 配置到 Sub2API 网关时使用。"
---

# Sub2API Codex Setup

使用 `npx -y sub2api-codex-setup --base-url <Sub2API地址> --api-key <API Key>` 完成 macOS、Windows 或 Linux 上的 Codex 客户端检测/安装和 Codex 配置写入。

先执行 `sub2api-codex-setup --help` 核对当前参数，执行 `sub2api-codex-setup --version` 确认版本。需要预演时加 `--dry-run`；只写配置、不安装客户端时加 `--no-install`。默认把 Key 写入 `config.toml` 的 `experimental_bearer_token`，无需额外设置环境变量；传入 `--auth-mode legacy` 时改用 `auth.json` 和 `SUB2API_API_KEY`。

该命令会修改用户目录下的 `~/.codex`（Windows 为 `%USERPROFILE%\.codex`）。不要替用户编造 API Key，不要把真实 Key 写入日志或提交到仓库。

通过 AIO 市场安装时，本文件随 npm 包自动分发到 `~/.agents/skills/sub2api-codex-setup/SKILL.md`；直接 npm/npx 安装不会自动写入个人技能目录。
