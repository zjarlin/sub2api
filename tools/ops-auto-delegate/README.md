# ops-auto-delegate —— 请求错误攒批后自动委托 252 的 Codex

把监控面板「请求错误」里的错误自动攒批，攒够一批就委托给本机（192.168.31.252）的 Codex：
**先逐条评估，需要改代码的直接修，不需要修的也给出判据**，全程不做部署、不重启容器。

## 工作原理

```
ops_error_logs (只读)  →  攒批/聚类  →  结构化提示词  →  codexhost delegate / codex exec
      ↑ 语义等价面板「请求错误」视图          ↑ 含【工作目录】+【AGENTS.md 硬性禁令】+【逐条评估要求】
```

1. **采集（只读）**：默认直接查生产库 `sub2api-postgres` 的 `ops_error_logs`，条件与面板
   「请求错误」视图一致（`status_code>=400 OR error_type='cyber_policy'`，且
   `is_business_limited=false`、`resolved=false`、排除 `is_count_tokens`）。
   面板里被归到「业务限制」的 503 突发噪音天然被排除。
2. **攒批**：按 `平台|阶段|类型|模型族|归一化消息` 聚成「错误签名」，累计未委托的错误条数/签名数。
3. **触发**：满足任一条件即委托一批（默认）：累计新错误 ≥ 5 条；或签名 ≥ 3 个且至少 3 条；
   或最早待处理错误已等待 ≥ 30 分钟。两次委托之间有 20 分钟冷却，避免批量风暴。
4. **委托**：把整批错误渲染成结构化提示词，交给 Codex。提示词里明确写死：

   - **工作目录**：`/opt/cloud-dev/zjarlin/aio/workspace/zjarlin/sub2api-chat-playground`
   - **硬性禁令**（来自本仓库 `AGENTS.md`）：禁止 `docker compose up ... sub2api`、禁止重启/重建容器、
     禁止自行部署；需要部署时只给建议，等用户确认后走 TeamCity 或 `deploy-252.sh`
   - **评估契约**：每条错误必须归入 `A 需改代码 / B 无需改代码 / C 信息不足` 之一并写判据；
     A 类要直接实现修复并补测试；报告写入 `tools/ops-auto-delegate/reports/<批次ID>.md`

## 快速开始

```bash
cd tools/ops-auto-delegate

# 1) 只看决策 + 生成提示词，不委托任何东西
node ops-auto-delegate.mjs --dry-run --since-id 195900 --json

# 2) 直接看完整提示词
node ops-auto-delegate.mjs --dry-run --print-prompt --since-id 195900

# 3) 真委托（在 Codex 会话内跑会自动用 codexhost delegate，线程出现在侧边栏）
node ops-auto-delegate.mjs

# 4) 无 Desktop 场景（systemd/cron）用 headless codex exec
node ops-auto-delegate.mjs --backend codex-exec
```

首次运行不会倒灌历史：默认从当前 `MAX(id)` 之后开始采集（`--since-id` 可覆盖）。
想补跑某段时间的积压就显式给 `--since-id`（如 `--since-id 195686 --lookback-minutes 240`）。

### 定时运行

```bash
cp deploy/ops-auto-delegate.service deploy/ops-auto-delegate.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now ops-auto-delegate.timer      # 每 10 分钟一次
systemctl start ops-auto-delegate.service           # 手动跑一次，journalctl -u 看日志
```

也可以直接挂 cron：

```
*/10 * * * * cd /opt/cloud-dev/zjarlin/aio/workspace/zjarlin/sub2api-chat-playground/tools/ops-auto-delegate && /usr/bin/node ops-auto-delegate.mjs --backend codex-exec >> reports/cron.log 2>&1
```

## 常用参数

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `--source sql\|api` | `sql` | 采集方式；`api` 需要 `SUB2API_ADMIN_API_KEY` |
| `--since-id <n>` | 状态文件 / 当前最大 id | 只处理 id 更大的错误 |
| `--lookback-minutes <n>` | 180 | 采集窗口 |
| `--min-events <n>` | 5 | 累计错误条数阈值 |
| `--min-signatures <n>` | 3 | 签名数阈值 |
| `--max-wait-minutes <n>` | 30 | 最早待处理错误的等待上限 |
| `--max-batch <n>` | 20 | 单批最多条数 |
| `--cooldown-minutes <n>` | 20 | 两次委托的最小间隔 |
| `--backend auto\|codexhost\|codex-exec` | `auto` | `auto`：有 `$CODEX_THREAD_ID` 用 codexhost，否则 codex-exec |
| `--harness <id>` | `codex` | codexhost harness |
| `--parent-thread <ref>` | `$CODEX_THREAD_ID` | codexhost 父线程 |
| `--model <m>` | harness 原生默认 | 指定委托使用的模型 |
| `--workdir <path>` | 仓库根 | 委托给 Codex 的工作目录 |
| `--ignore <regex>` | 无 | 忽略匹配的错误签名（可重复） |
| `--auto-resolve` | 关 | 委托后把本批标记为已解决（**写生产库**，默认关闭） |
| `--dry-run` / `--print-prompt` / `--json` | — | 只出决策 / 打印提示词 / JSON 输出 |

## 状态与产物

- 状态：`$XDG_STATE_HOME/sub2api-ops-auto-delegate/state.json`（默认 `~/.local/state/...`）
  记录 `lastId` 与已委托批次（错误 id、签名、线程、时间），同一批不会重复委托；
  文件锁保证单实例，冷却期内不会连续委托。
- 产物：`tools/ops-auto-delegate/reports/`（已 gitignore）
  - `<批次ID>.prompt.md`：本次委托的完整提示词（可追溯）
  - `<批次ID>.md`：被委托的 Codex 写入的评估/修复报告
  - `<批次ID>.last-message.md`：`codex-exec` 模式的最后一条回复

## 安全边界

- 脚本自身**只读**：只查 `ops_error_logs`，不写库、不动容器；只有显式 `--auto-resolve` 才写生产库。
- 提示词里写死 `AGENTS.md` 的硬性禁令，被委托的 Codex 不许部署/重启/重建容器。
- 被委托的 Codex 只在分支 `ops-fix/<批次ID>` 上提交改动，不 push 主分支、不发布。

## 说明与边界

- 采集的是「client-visible 请求错误」，与面板一致；`business_limited`（业务限制）和
  `recovered_upstream`（已恢复）不在其中，这两类不应触发代码修复。
- 面板里的「一键复制」提示词是**给人看、给 AI 建议**的（`frontend/src/views/admin/ops/utils/buildFixPrompt.ts`）；
  本工具复用同一套字段与归类口径，但要求被委托方**真的动手改代码并补测试**。
- 更彻底的形态是把「错误 → 委托」做成后端告警通道（进度条在后端 alert rule / notifier 里），
  但那需要改后端并走部署流水线；当前脚本形式不需要部署，可以立即生效。
