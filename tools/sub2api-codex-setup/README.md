# Sub2API Codex Setup

## 快速安装

```sh
npx -y sub2api-codex-setup --base-url https://your-sub2api.example.com --api-key sk-xxxx
```

默认安装官方桌面客户端。需要 Node.js 22.14 或更新版本；Linux 可用 `--client cli` 安装官方 Codex CLI。

Windows CMD / PowerShell 使用：

```powershell
npx.cmd -y sub2api-codex-setup --base-url https://your-sub2api.example.com --api-key sk-xxxx
```

`npx.cmd` 避免 PowerShell 因执行策略阻止 `npx.ps1`。工具按 Node.js 的运行平台选择安装器，Windows 使用 `winget` 安装官方桌面应用；缺少 `winget` 或安装失败时，自动下载并运行官方 Windows EXE。安装失败会退出，不写配置。客户端下载仅需安装应用时也可直接使用官方命令 `winget install --id 9PLM9XGG6VKS -s msstore`，无需下载 macOS DMG。

## 安装到其他磁盘

Windows CMD / PowerShell 示例（CLI 程序与配置、会话数据均放在 G 盘）：

```powershell
npx.cmd -y sub2api-codex-setup --base-url https://your-sub2api.example.com --api-key sk-xxxx --client cli --install-dir "G:\Codex\app" --codex-home "G:\Codex\data" --persist-home
```

- `--client cli`：通过 npm 安装官方 Codex CLI；默认仍为 `desktop`。
- `--install-dir`：绝对路径。CLI 模式作为 npm 安装前缀，安装时的 npm 缓存也放在该目录下。Windows 自动加入用户 PATH，重新打开终端后生效；macOS/Linux 输出 PATH 设置命令。
- `--codex-home`：配置、认证和模型目录写入此绝对路径，优先于已有 `CODEX_HOME`；均未指定时使用用户目录的 `.codex`。
- `--persist-home`：Windows 专用，将显式 `--codex-home` 保存到用户环境变量。重新打开终端并重启应用；启动器仍保留旧环境时需注销后重新登录。未使用此选项时，CLI 输出启动 Codex 前需要执行的环境变量命令。
- `--no-install`：仅写配置，不能同时指定 `--install-dir`。
- `--dry-run`：只显示计划，不安装、不写文件或用户环境变量。

已有配置文件会被替换。切换数据目录不会自动搬迁旧会话；Unix 用户需按输出设置 `CODEX_HOME` 并加入 shell profile。操作系统临时文件及运行本工具的 npx 缓存仍遵循系统/npm 设置；若也要更改 npx 缓存，可在上面的 `npx` 后加 `--cache 'D:\Codex\setup-cache'`。

Windows 商店版桌面应用不支持任意 `--install-dir`，CLI 会明确拒绝该组合。请在「设置 → 系统 → 存储 → 新内容的保存位置」选择应用安装盘；已安装应用可在「已安装的应用」中查看是否支持移动。macOS 桌面版支持 `--install-dir /Volumes/Data/Applications`。`--codex-home` 只控制 Codex 数据，不会移动桌面应用自身的缓存。

`--codex-home` 和 `--persist-home` 从 0.1.3 起提供；0.1.2 会报 `Unknown option '--codex-home'`，与盘符无关。上面的 D/G 盘没有默认值，任意已挂载且有写权限的盘符均可。CMD 不识别单引号，普通路径请用双引号；包含 PowerShell 特殊字符的路径应在 PowerShell 中使用单引号。

## 自定义安装源

需要安装魔改版时，只需指定安装源：

```sh
npx -y sub2api-codex-setup \
  --base-url https://your-sub2api.example.com \
  --api-key sk-xxxx \
  --install-source modified
```

默认从 `zjarlin/sub2api` 的 GitHub Release 最新版下载脚本：

- macOS / Linux：`codex-install.sh`
- Windows：`codex-install.ps1`

发布魔改版时，需要把对应脚本作为 GitHub Release asset 上传到仓库。需要换仓库或使用私有镜像时，可传 `--modified-installer-url <url>`，或设置 `SUB2API_CODEX_MODIFIED_INSTALL_URL`；也可用 `SUB2API_CODEX_MODIFIED_INSTALL_REPO=<owner/repo>` 只替换 GitHub 仓库。CLI 会下载并执行脚本，随后写入 Sub2API 配置；只校验 URL 协议，不校验脚本内容。
