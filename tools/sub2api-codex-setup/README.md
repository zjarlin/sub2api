# Sub2API Codex Setup

## 快速安装

```sh
npx -y sub2api-codex-setup --base-url https://your-sub2api.example.com --api-key sk-xxxx
```

默认安装官方 Codex 客户端。需要安装魔改版时，只需指定安装源：

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
