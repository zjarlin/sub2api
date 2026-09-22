# Sub2API Codex Setup

One command to install the Codex desktop client when needed and write the Codex CLI configuration for a Sub2API key.

```sh
npx -y sub2api-codex-setup --base-url https://your-sub2api.example.com --api-key sk-xxxx
```

The CLI detects the current operating system:

- macOS: skips installation when `Codex.app` or `ChatGPT.app` is already present; otherwise opens the official Codex DMG.
- Windows: skips installation when the Codex Store app is already present; otherwise runs `winget install Codex -s msstore`.
- Linux: no desktop installer is available, so the command writes configuration only.

Configuration is written to `~/.codex/config.toml` on macOS/Linux and `%USERPROFILE%\.codex\config.toml` on Windows. By default the key is written into `config.toml` as `experimental_bearer_token` so no shell environment variable is required. `--auth-mode legacy` writes `auth.json` instead and uses `SUB2API_API_KEY`.

## Options

```sh
npx -y sub2api-codex-setup \
  --base-url https://your-sub2api.example.com \
  --api-key sk-xxxx \
  --model gpt-5.5 \
  --auth-mode api-key
```

- `--base-url`: Sub2API base URL, with or without `/v1`.
- `--api-key`: Sub2API API key.
- `--model`: Codex model, default `gpt-5.5`.
- `--provider-name`: Provider name written to `config.toml`, default `Sub2API`.
- `--auth-mode`: `api-key` writes `experimental_bearer_token` into `config.toml`; `legacy` writes `auth.json` and uses `SUB2API_API_KEY`.
- `--no-install`: Skip client installation and only write configuration.
- `--dry-run`: Print detected platform and planned actions without changing the machine.

## Development

```sh
npm ci
npm test
node dist/cli.mjs --base-url https://example.com --api-key sk-test --dry-run
```

This package is initialized and released through AIO. See [AIO.md](AIO.md) for npm Trusted Publisher and plugin marketplace release setup.
