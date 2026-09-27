---
name: sub2api-codex-setup
description: "当用户需要一键安装 Codex 客户端并把 Codex CLI 配置到 Sub2API 网关时使用。"
---

# Sub2API Codex Setup

使用 `npx -y sub2api-codex-setup --base-url <Sub2API地址> --api-key <API Key>` 完成 macOS、Windows 或 Linux 上的 Codex 客户端检测/安装和 Codex 配置写入。

先执行 `sub2api-codex-setup --help` 核对当前参数，执行 `sub2api-codex-setup --version` 确认版本。需要预演时加 `--dry-run`；只写配置、不安装客户端时加 `--no-install`。默认安装官方客户端；安装魔改版时加 `--install-source modified`，CLI 会从 `zjarlin/sub2api` 的 GitHub Release 最新版下载 `codex-install.sh` 或 `codex-install.ps1`。需要换仓库或镜像时使用 `--modified-installer-url`、`SUB2API_CODEX_MODIFIED_INSTALL_URL` 或 `SUB2API_CODEX_MODIFIED_INSTALL_REPO`。魔改版脚本会直接执行，使用前必须确认来源可信。默认把 Key 写入 `config.toml` 的 `experimental_bearer_token`，无需额外设置环境变量；传入 `--auth-mode legacy` 时改用 `auth.json` 和 `SUB2API_API_KEY`。

默认修改 `CODEX_HOME` 指向的目录，未设置时使用 `~/.codex`（Windows 为 `%USERPROFILE%\.codex`）；`--codex-home` 可指定其他绝对路径。Windows 加 `--persist-home` 将该路径保存到用户环境变量，随后重开终端和应用。旧会话不会自动迁移。不要替用户编造 API Key，不要把真实 Key 写入日志或提交到仓库。

用户需要安装到 D 盘时，可使用 `--client cli --install-dir 'D:\Codex\app' --codex-home 'D:\Codex\data' --persist-home`。CLI 程序和安装缓存放在指定安装目录，Windows 用户 PATH 自动更新。Windows 商店版桌面应用的安装盘由系统存储设置管理，不支持任意 `--install-dir`；macOS 桌面版支持该参数。Linux 选择 `--client cli`；选择桌面版时仅写配置。

通过 AIO 市场安装时，本文件随 npm 包自动分发到 `~/.agents/skills/sub2api-codex-setup/SKILL.md`；直接 npm/npx 安装不会自动写入个人技能目录。
