#!/usr/bin/env node
/**
 * ops-auto-delegate —— sub2api「请求错误」攒批后自动委托 252 的 Codex 评估/修复。
 *
 * 工作方式（只读采集 → 攒批 → 委托）：
 *   1. 只读从 sub2api 生产库 ops_error_logs 采集 client-visible 请求错误
 *      （语义与监控面板「请求错误」一致：status_code>=400 OR error_type=cyber_policy，
 *        且 is_business_limited=false、resolved=false、可选排除 count_tokens）。
 *   2. 按「平台|阶段|类型|模型族|归一化消息」聚成签名，累计未委托的错误。
 *   3. 满足触发条件（条数 / 签名数 / 最长等待）时，把整批错误渲染成结构化提示词，
 *      委托给本机 Codex（默认 codexhost delegate，回退 codex exec），
 *      提示词里包含【工作目录】【AGENTS.md 硬性禁令】【逐条评估要求】。
 *   4. 不需要修的也要求逐条给出判据；需要改代码的直接实现并补测试。
 *
 * 本脚本自身不做任何写操作；除非显式传入 --auto-resolve，否则不写生产库、不部署、不重启容器。
 *
 * 用法见同目录 README.md；`--dry-run` 只打印决策与提示词。
 */

import { spawnSync } from 'node:child_process'
import crypto from 'node:crypto'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = path.resolve(HERE, '..', '..')

// ---------------------------------------------------------------------------
// 默认值
// ---------------------------------------------------------------------------
const DEFAULTS = {
  source: 'sql', // sql | api
  dbContainer: 'sub2api-postgres',
  dbUser: 'sub2api',
  dbName: 'sub2api',
  apiBase: 'http://127.0.0.1:18080',
  apiKeyEnv: 'SUB2API_ADMIN_API_KEY',
  lookbackMinutes: 180,
  minEvents: 5,
  minSignatures: 3,
  maxWaitMinutes: 30,
  maxBatch: 20,
  cooldownMinutes: 20,
  backend: 'auto', // auto | codexhost | codex-exec
  harness: 'codex',
  parentThread: '',
  model: 'auto', // 网关 Auto Router；置空字符串则用 harness 原生默认模型
  fallbackModel: 'deepseek-v4.1-flash', // 委托失败（如 auto 路由对带工具的 responses 报 503）时的重试模型
  selfNoise: true, // 过滤「委托自己产生的」错误，避免自激循环
  hostIps: [], // 留空自动探测本机 IP / docker 网关
  worktree: false, // codexhost 的 Harness 不认 --worktree，默认关闭
  codexBin: '', // 留空自动定位官方 codex 二进制（绕开 codexhost shim）
  // 被测方若直接在共享检出上切分支/改文件会污染工作区（实测发生过两次），
  // 所以每次委托都自建一个独立 git worktree，让 Codex 只在那里动手。
  worktreesDir: path.join(os.tmpdir(), 'sub2api-ops-fix'),
  gitBin: '', // 留空自动挑选 >= 2.5 的 git（/usr/bin/git 是 1.8，不支持 worktree）
  baseRef: '', // 首次运行自动记录 cfg.workdir 当时的 HEAD 作为基线
  keepWorktrees: 5,
  statePath: path.join(
    process.env.XDG_STATE_HOME || path.join(os.homedir(), '.local', 'state'),
    'sub2api-ops-auto-delegate',
    'state.json'
  ),
  reportsDir: path.join(HERE, 'reports'),
  workdir: REPO_ROOT,
  ignorePatterns: [],
  autoResolve: false,
  dryRun: false,
  json: false,
  printPrompt: false,
  once: true,
}

// ---------------------------------------------------------------------------
// 参数解析
// ---------------------------------------------------------------------------
function parseArgs(argv) {
  const cfg = { ...DEFAULTS }
  const num = (v, name) => {
    const n = Number(v)
    if (!Number.isFinite(n) || n < 0) throw new Error(`参数 ${name} 需要非负数字，收到 ${v}`)
    return n
  }
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]
    const next = () => {
      if (i + 1 >= argv.length) throw new Error(`参数 ${a} 缺少值`)
      return argv[++i]
    }
    switch (a) {
      case '--source': cfg.source = next(); break
      case '--config': cfg.configPath = next(); break
      case '--since-id': cfg.sinceId = num(next(), a); break
      case '--lookback-minutes': cfg.lookbackMinutes = num(next(), a); break
      case '--min-events': cfg.minEvents = num(next(), a); break
      case '--min-signatures': cfg.minSignatures = num(next(), a); break
      case '--max-wait-minutes': cfg.maxWaitMinutes = num(next(), a); break
      case '--max-batch': cfg.maxBatch = num(next(), a); break
      case '--cooldown-minutes': cfg.cooldownMinutes = num(next(), a); break
      case '--backend': cfg.backend = next(); break
      case '--harness': cfg.harness = next(); break
      case '--parent-thread': cfg.parentThread = next(); break
      case '--model': cfg.model = next(); break
      case '--codex-bin': cfg.codexBin = next(); break
      case '--worktrees-dir': cfg.worktreesDir = path.resolve(next()); break
      case '--git-bin': cfg.gitBin = next(); break
      case '--fallback-model': cfg.fallbackModel = next(); break
      case '--no-self-noise-filter': cfg.selfNoise = false; break
      case '--host-ip': cfg.hostIps.push(next()); break
      case '--base-ref': cfg.baseRef = next(); break
      case '--keep-worktrees': cfg.keepWorktrees = num(next(), a); break
      case '--no-worktree': cfg.worktree = false; break
      case '--worktree': cfg.worktree = true; break
      case '--state': cfg.statePath = path.resolve(next()); break
      case '--reports-dir': cfg.reportsDir = path.resolve(next()); break
      case '--workdir': cfg.workdir = path.resolve(next()); break
      case '--db-container': cfg.dbContainer = next(); break
      case '--api-base': cfg.apiBase = next(); break
      case '--ignore': cfg.ignorePatterns.push(next()); break
      case '--auto-resolve': cfg.autoResolve = true; break
      case '--dry-run': cfg.dryRun = true; break
      case '--print-prompt': cfg.printPrompt = true; break
      case '--json': cfg.json = true; break
      case '-h':
      case '--help': cfg.help = true; break
      default:
        throw new Error(`未知参数 ${a}（-h 查看用法）`)
    }
  }
  return cfg
}

const HELP = `ops-auto-delegate —— 请求错误攒批自动委托 Codex 评估/修复

  --source sql|api          错误来源（默认 sql=只读查生产库；api=管理接口）
  --config <path>           读取 JSON 配置（默认 tools/ops-auto-delegate/config.json，存在才生效）
  --since-id <n>            只处理 id 大于该值的错误；首次运行默认取当前最大 id（不倒灌历史）
  --lookback-minutes <n>    采集窗口，默认 180
  --min-events <n>          触发委托的累计新错误数，默认 5
  --min-signatures <n>      触发委托的最少错误签名数，默认 2
  --max-wait-minutes <n>    最早待处理错误超过该分钟数即触发，默认 30
  --max-batch <n>           单批最多纳入条数，默认 20
  --cooldown-minutes <n>    两次委托的最小间隔，默认 20
  --backend auto|codexhost|codex-exec   默认 auto
  --harness <id>            codexhost harness，默认 codex
  --parent-thread <ref>     codexhost 父线程；默认取 $CODEX_THREAD_ID
  --model <m>               透传给 Codex 的模型（默认 auto=网关 Auto Router；传空串则用 harness 原生默认）
  --codex-bin <path>        codex-exec 后端使用的官方 codex 二进制（默认自动定位，避开 codexhost shim）
  --worktrees-dir <path>    每次委托自建 worktree 的父目录，默认 $TMPDIR/sub2api-ops-fix
  --git-bin <path>          使用的 git 可执行文件（默认自动挑 >= 2.5；1.8 不支持 worktree）
  --fallback-model <m>      委托失败（如 auto 路由 503）时重试一次用的模型，默认空=harness 原生默认
  --no-self-noise-filter    不过滤「本次委托自己产生的」错误（默认过滤，防自激循环）
  --host-ip <ip>            额外视为本机的 IP（可重复；默认自动探测）
  --base-ref <ref>          worktree 的基线 ref，默认首次运行时记录的工作区 HEAD
  --keep-worktrees <n>      保留最近 n 个 worktree，其余自动清理，默认 5
  --no-worktree             不要求 Harness 使用独立 worktree（默认即关闭；Harness 支持时可用 --worktree 打开）
  --state <path>            状态文件，默认 $XDG_STATE_HOME/sub2api-ops-auto-delegate/state.json
  --reports-dir <path>      报告目录，默认 tools/ops-auto-delegate/reports
  --workdir <path>          委托给 Codex 的工作目录，默认仓库根
  --db-container <name>     默认 sub2api-postgres
  --api-base <url>          默认 http://127.0.0.1:18080
  --ignore <regex>          忽略匹配该正则的错误签名（可重复）
  --auto-resolve            委托成功后把本批错误在库里标记为已解决（写生产库，默认关闭）
  --dry-run                 只输出决策与提示词，不委托
  --print-prompt            打印完整提示词
  --json                    以 JSON 输出决策结果
`

// ---------------------------------------------------------------------------
// 配置合并
// ---------------------------------------------------------------------------
function loadConfig(cfg) {
  const p = cfg.configPath || path.join(HERE, 'config.json')
  if (!fs.existsSync(p)) return cfg
  const raw = JSON.parse(fs.readFileSync(p, 'utf8'))
  const merged = { ...cfg }
  for (const [k, v] of Object.entries(raw)) {
    if (v === undefined || v === null) continue
    if (k === 'ignorePatterns') merged.ignorePatterns = [...merged.ignorePatterns, ...v]
    else if (!(k in DEFAULTS)) continue
    else if (Array.isArray(raw[k])) continue
    // 命令行显式传入的（非默认值）优先
    else if (cfg[k] === DEFAULTS[k] || cfg[k] === undefined) merged[k] = v
  }
  return merged
}

// ---------------------------------------------------------------------------
// 状态：lastId / 待处理批次 / 已委托记录 + 单实例锁
// ---------------------------------------------------------------------------
function withLock(statePath, fn) {
  const lockPath = `${statePath}.lock`
  fs.mkdirSync(path.dirname(statePath), { recursive: true })
  let fd
  try {
    fd = fs.openSync(lockPath, 'wx')
    fs.writeSync(fd, String(process.pid))
  } catch (e) {
    if (e.code === 'EEXIST') {
      const pid = Number(fs.readFileSync(lockPath, 'utf8').trim())
      let alive = false
      try { process.kill(pid, 0); alive = true } catch { alive = false }
      if (alive) throw new Error(`已有实例在运行（pid=${pid}，锁文件 ${lockPath}）`)
      fs.unlinkSync(lockPath)
      fd = fs.openSync(lockPath, 'wx')
      fs.writeSync(fd, String(process.pid))
    } else throw e
  }
  try {
    return fn()
  } finally {
    try { fs.closeSync(fd) } catch {}
    try { fs.unlinkSync(lockPath) } catch {}
  }
}

// 从历史委托记录推导「委托窗口」，用于识别委托自身产生的错误
function collectDelegationWindows(state) {
  const out = []
  for (const d of state.delegated || []) {
    const start = Date.parse(d.startedAt || d.at)
    const end = Date.parse(d.at)
    if (Number.isFinite(start) && Number.isFinite(end)) out.push({ start, end })
  }
  return out
}

function loadState(statePath) {
  if (!fs.existsSync(statePath)) return { lastId: 0, delegated: [], worktrees: [], baseRef: '' }
  const s = JSON.parse(fs.readFileSync(statePath, 'utf8'))
  return {
    lastId: s.lastId || 0,
    delegated: Array.isArray(s.delegated) ? s.delegated : [],
    worktrees: Array.isArray(s.worktrees) ? s.worktrees : [],
    baseRef: s.baseRef || '',
  }
}

function saveState(statePath, state) {
  fs.mkdirSync(path.dirname(statePath), { recursive: true })
  // 只保留最近 200 条委托记录
  state.delegated = state.delegated.slice(-200)
  const tmp = `${statePath}.tmp`
  fs.writeFileSync(tmp, JSON.stringify(state, null, 2))
  fs.renameSync(tmp, statePath)
}

// ---------------------------------------------------------------------------
// 采集：只读 SQL（默认） 或 管理接口
// ---------------------------------------------------------------------------
const SELECT_SQL = (sinceId, lookbackMinutes) => `
SELECT COALESCE(json_agg(row_to_json(t) ORDER BY t.id), '[]'::json)::text
FROM (
  SELECT
    e.id,
    to_char(e.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD HH24:MI:SS') AS at,
    COALESCE(e.error_phase, '')            AS phase,
    COALESCE(e.error_type, '')             AS type,
    COALESCE(e.severity, '')               AS severity,
    COALESCE(e.status_code, 0)             AS status_code,
    COALESCE(e.platform, '')               AS platform,
    COALESCE(NULLIF(TRIM(e.requested_model), ''), e.model, '') AS model,
    COALESCE(e.upstream_model, '')         AS upstream_model,
    COALESCE(e.account_id, 0)              AS account_id,
    COALESCE(e.group_id, 0)                AS group_id,
    COALESCE(e.error_owner, '')            AS owner,
    COALESCE(e.error_source, '')           AS source,
    COALESCE(e.error_message, '')          AS message,
    COALESCE(e.error_body, '')             AS body,
    COALESCE(e.upstream_status_code, 0)    AS upstream_status_code,
    COALESCE(e.upstream_error_message, '') AS upstream_message,
    COALESCE(e.upstream_error_detail, '')  AS upstream_detail,
    COALESCE(e.upstream_errors::text, '')  AS attempt_chain,
    COALESCE(e.inbound_endpoint, '')       AS inbound_endpoint,
    COALESCE(e.upstream_endpoint, '')      AS upstream_endpoint,
    COALESCE(e.request_id, '')             AS request_id,
    COALESCE(e.client_ip::text, '')        AS client_ip,
    COALESCE(e.user_agent, '')             AS user_agent,
    COALESCE(e.duration_ms, 0)             AS duration_ms
  FROM ops_error_logs e
  WHERE e.id > ${Number(sinceId)}
    AND e.created_at >= now() - interval '${Number(lookbackMinutes)} minutes'
    AND (COALESCE(e.status_code, 0) >= 400 OR e.error_type = 'cyber_policy')
    AND COALESCE(e.is_business_limited, false) = false
    AND COALESCE(e.resolved, false) = false
    AND COALESCE(e.is_count_tokens, false) = false
  ORDER BY e.id ASC
  LIMIT 500
) t;
`

function fetchFromSql(cfg, sinceId) {
  const args = [
    'exec', '-i', cfg.dbContainer,
    'psql', '-U', cfg.dbUser, '-d', cfg.dbName,
    '-X', '-A', '-t', '-v', 'ON_ERROR_STOP=1', '-f', '-',
  ]
  const sql = SELECT_SQL(sinceId, cfg.lookbackMinutes)
  if (process.env.OPS_AUTO_DELEGATE_DEBUG === '1') process.stderr.write(`[debug] SQL:\n${sql}\n`)
  const res = spawnSync('docker', args, {
    input: sql,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  })
  if (process.env.OPS_AUTO_DELEGATE_DEBUG === '1') process.stderr.write(`[debug] exit=${res.status} stdout=${(res.stdout||'').slice(0,500)} stderr=${res.stderr}\n`)
  if (res.error) throw new Error(`无法执行 docker exec：${res.error.message}`)
  if (res.status !== 0) throw new Error(`psql 失败（exit ${res.status}）：${(res.stderr || '').trim()}`)
  const out = (res.stdout || '').trim()
  if (!out) return []
  return JSON.parse(out)
}

function fetchFromApi(cfg, sinceId) {
  const key = process.env[cfg.apiKeyEnv]
  if (!key) throw new Error(`--source api 需要环境变量 ${cfg.apiKeyEnv}`)
  const url = new URL('/api/v1/admin/ops/request-errors', cfg.apiBase)
  url.searchParams.set('page', '1')
  url.searchParams.set('page_size', '200')
  url.searchParams.set('view', 'errors')
  url.searchParams.set('resolved', '0')
  const res = spawnSync('curl', [
    '-sS', '-H', `x-api-key: ${key}`, '-H', `Authorization: Bearer ${key}`, url.toString(),
  ], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
  if (res.status !== 0) throw new Error(`curl 失败：${(res.stderr || '').trim()}`)
  const body = JSON.parse(res.stdout)
  if (body.code && body.code !== 'OK' && !Array.isArray(body.items)) {
    throw new Error(`管理接口返回 ${body.code}: ${body.message}`)
  }
  const items = body.items || body.data || []
  return items
    .filter((e) => Number(e.id) > Number(sinceId))
    .map((e) => ({
      id: Number(e.id),
      at: e.created_at || '',
      phase: e.phase || e.error_phase || '',
      type: e.type || e.error_type || '',
      severity: e.severity || '',
      status_code: Number(e.status_code || 0),
      platform: e.platform || '',
      model: e.model || '',
      upstream_model: e.upstream_model || '',
      account_id: Number(e.account_id || 0),
      group_id: Number(e.group_id || 0),
      owner: e.error_owner || '',
      source: e.error_source || '',
      message: e.message || e.error_message || '',
      body: e.error_body || '',
      upstream_status_code: Number(e.upstream_status_code || 0),
      upstream_message: e.upstream_error_message || '',
      upstream_detail: e.upstream_error_detail || '',
      attempt_chain: e.upstream_errors || '',
      inbound_endpoint: e.inbound_endpoint || '',
      upstream_endpoint: e.upstream_endpoint || '',
      request_id: e.request_id || '',
      client_ip: e.client_ip || '',
      user_agent: e.user_agent || '',
      duration_ms: Number(e.duration_ms || 0),
    }))
    .sort((a, b) => a.id - b.id)
}

// ---------------------------------------------------------------------------
// 聚类与触发
// ---------------------------------------------------------------------------
function normalizeMessage(msg) {
  return String(msg || '')
    .replace(/\s+/g, ' ')
    .replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi, '<uuid>')
    .replace(/\d+/g, '#')
    .trim()
    .slice(0, 120)
}

function modelFamily(model) {
  return String(model || '')
    .toLowerCase()
    .replace(/\d+/g, '#')
    .replace(/#[#.\-_]*/g, '#')
    .replace(/[.\-_]+$/g, '')
    .slice(0, 60)
}

function signatureOf(e) {
  return [e.platform, e.phase, e.type, modelFamily(e.model), normalizeMessage(e.message)].join('|')
}

function clusterErrors(errors) {
  const groups = new Map()
  for (const e of errors) {
    const sig = signatureOf(e)
    if (!groups.has(sig)) groups.set(sig, { signature: sig, items: [] })
    groups.get(sig).items.push(e)
  }
  return [...groups.values()].sort((a, b) => b.items.length - a.items.length)
}

function shouldTrigger(clusters, errors, cfg) {
  const reasons = []
  const totalEvents = errors.length
  if (totalEvents >= cfg.minEvents) reasons.push(`累计新错误 ${totalEvents} 条 ≥ ${cfg.minEvents}`)
  if (clusters.length >= cfg.minSignatures && totalEvents >= Math.max(3, Math.min(cfg.minSignatures, cfg.minEvents))) {
    reasons.push(`错误签名 ${clusters.length} 个 ≥ ${cfg.minSignatures}`)
  }
  const oldest = errors[0]
  if (oldest) {
    const ageMin = (Date.now() - new Date(oldest.at.replace(' ', 'T') + '+08:00').getTime()) / 60000
    if (Number.isFinite(ageMin) && ageMin >= cfg.maxWaitMinutes) {
      reasons.push(`最早待处理错误已 ${ageMin.toFixed(0)} 分钟 ≥ ${cfg.maxWaitMinutes}`)
    }
  }
  return reasons
}

// ---------------------------------------------------------------------------
// 提示词
// ---------------------------------------------------------------------------
const STAGE_ZH = { routing: '路由', account_auth: '账号认证', vision_helper: '视觉辅助', upstream: '上游' }

function collapse(text, max) {
  const s = String(text || '').replace(/\s+/g, ' ').trim()
  return s.length > max ? `${s.slice(0, max)}...` : s
}

function renderError(e, i) {
  const lines = [
    `■ 错误 #${i + 1} [id=${e.id}] ${e.at}`,
    `- HTTP ${e.status_code} | 阶段=${STAGE_ZH[e.phase] || e.phase}/${e.type} | 归属=${e.owner || '-'} | 来源=${e.source || '-'} | 严重度=${e.severity || '-'}`,
    `- 平台=${e.platform || '-'} | 模型=${e.model || '-'}${e.upstream_model && e.upstream_model !== e.model ? ` → 上游模型=${e.upstream_model}` : ''} | 账号=${e.account_id || '-'} | 分组=${e.group_id || '-'} | 耗时=${e.duration_ms || '-'}ms`,
    `- 端点：${e.inbound_endpoint || '-'} → ${e.upstream_endpoint || '-'} | request_id=${e.request_id || '-'}`,
    `- 消息：${collapse(e.message, 240) || '-'}`,
    `- 错误体：${collapse(e.body, 400) || '-'}`,
  ]
  if (e.upstream_status_code) lines.push(`- 上游状态码：${e.upstream_status_code}`)
  if (e.upstream_message) lines.push(`- 上游错误信息：${collapse(e.upstream_message, 240)}`)
  if (e.upstream_detail) lines.push(`- 上游错误详情：${collapse(e.upstream_detail, 400)}`)
  lines.push(`- 调用链(原始 JSON)：${collapse(e.attempt_chain, 1200) || '无'}`)
  return lines.join('\n')
}

function buildPrompt(cfg, batch, ctx) {
  const { errors, clusters, reasons, batchKey } = batch
  const batchId = batchKey.slice(0, 12)
  const workdir = ctx.workdir
  const branch = ctx.branch
  const base = ctx.base
  const reportsDir = path.join(cfg.workdir, 'tools', 'ops-auto-delegate', 'reports')
  const lines = []
  lines.push(
    '你是资深后端工程师。运维监控已累积一批 sub2api 网关的「请求错误」日志，请对每一批错误先评估、再按需修复。',
    '',
    '【第一步：先读工作区根目录 AGENTS.md】',
    `工作目录（独立 worktree，已切到分支 ${branch}）：${workdir}`,
    `- 这是一个 **临时 git worktree**，基线为 ${base}；请在当前分支上提交改动，不要切换/删除分支。`,
    `- **绝对不要**操作 /opt/sub2api 或共享检出 ${cfg.workdir} 的 git 状态，也不要 push。`,
    '',
    ...(ctx.openBranches && ctx.openBranches.length
      ? [
          '【已在办的修复分支（不要重复排查，先看是否已覆盖本批，必要时补充/纠正）】',
          ...ctx.openBranches.map((b) => `- ${b.name}：${b.subject || '(无提交信息)'}`),
          '',
        ]
      : []),
    '【硬性禁令（来自该仓库 AGENTS.md，必须遵守）】',
    '- 禁止执行任何 `docker compose up ... sub2api`（含 -d / --no-deps / --force-recreate）与任何等价写法。',
    '- 禁止重启、重建、替换任何容器；禁止自行部署镜像或改动生产 Compose/环境变量。',
    '- 需要部署才能验证时：只在报告里给出建议与理由，等用户明确同意后走 TeamCity 流水线 `sub2api / Deploy 252 Cluster` 或 `/opt/sub2api/deploy/cluster/deploy-252.sh`。',
    '- 只读排查不受限：docker ps/logs/inspect、curl 127.0.0.1:18080/health、git、读日志。',
    `- 不要 push；改动就在当前 worktree 的 ${branch} 分支上提交，是否合并/发布由用户决定。`,
    '',
    '【本批触发信息】',
    `- 批次ID：${batchId}`,
    `- 触发原因：${reasons.join('；')}`,
    `- 错误条数：${errors.length}；错误签名（平台|阶段|类型|模型族|归一化消息）个数：${clusters.length}`,
    `- 采集窗口：最近 ${cfg.lookbackMinutes} 分钟；状态：resolved=false、is_business_limited=false、status>=400（等价监控面板「请求错误」视图）`,
    '',
    `【报错日志与调用链（共 ${errors.length} 条）】`,
    ''
  )
  errors.forEach((e, i) => { lines.push(renderError(e, i), '') })

  lines.push(
    '【签名聚合（便于定位系统性缺陷）】',
    ...clusters.map((c) => `- ${c.signature}  ×${c.items.length}  (id: ${c.items.map((x) => x.id).join(',')})`),
    '',
    '【任务要求】',
    '1. 逐条评估每一条错误，给出结论，且必须归入下列三类之一，并写出判据：',
    '   - A 需改代码（平台自身缺陷）：定位文件/函数/逻辑分支，给出根因。',
    '   - B 无需改代码（配置/账号/上游/客户端问题）：说明为什么改代码不成立，以及正确的处理动作（例如补账号、改分组模型目录、上游报障、让客户端调整请求）。',
    '   - C 信息不足：说明还需要什么证据（具体查询/日志/复现步骤）。',
    '2. 对 A 类：直接实现修复并补上对应测试；遵循现有架构与代码风格，避免无关改动。',
    '3. 不要执行任何部署/发布/容器重建动作。',
    `4. 在 \`${reportsDir}/${batchId}.md\` 写入结构化报告。注意该目录已被 .gitignore 忽略，` +
      `提交时用 \`git add -f\` 显式加入，保证报告随分支留档：`,
    '   - 表格：id / 结论(A|B|C) / 一句话根因 / 依据 / 建议动作',
    '   - A 类的补丁摘要与验证方式（跑过哪些测试/命令及结果）',
    '   - B 类汇总（可合并为「环境问题清单」）',
    '5. 最后在会话里用中文给出简短结论摘要：A/B/C 各多少条、改了哪些文件、是否提交、下一步需要用户做什么。'
  )
  return lines.join('\n')
}

// ---------------------------------------------------------------------------
// 委托
// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// 自激循环防护：委托过程中被委托方自己发出的请求也会写 ops_error_logs，
// 若不排除，下一轮会把这些「自己造的错误」当成新错误再委托，形成无限循环。
// 判据（时间窗 + 来源）：落在已记录的委托窗口内，且来源是本机 IP 或 codex 客户端 UA。
// ---------------------------------------------------------------------------
function detectHostIps() {
  const ips = new Set(['127.0.0.1', '::1'])
  const res = spawnSync('hostname', ['-I'], { encoding: 'utf8' })
  if (res.status === 0) {
    for (const ip of (res.stdout || '').trim().split(/\s+/)) if (ip) ips.add(ip)
  }
  // docker 网桥网关（容器内探测打到宿主时的常见来源）
  ips.add('172.17.0.1')
  ips.add('172.21.0.1')
  return [...ips]
}

const CODEX_UA_RE = /codex[_-]?(exec|cli|vscode|app)|curl\//i

function isSelfNoise(e, cfg, hostIps, windows) {
  if (!cfg.selfNoise) return false
  const ua = String(e.user_agent || '')
  const ip = String(e.client_ip || '')
  const fromHost = hostIps.includes(ip) || hostIps.some((h) => h && ip.startsWith(h))
  if (!fromHost || !CODEX_UA_RE.test(ua)) return false
  // 只有落在某次委托的时间窗内才算「委托自身的噪音」；
  // 窗口之外的本机 codex 流量（例如用户自己的 Codex 会话）仍按真实错误处理。
  if (!windows || !windows.length) return false
  const t = Date.parse(String(e.at || '').replace(' ', 'T') + '+08:00')
  if (!Number.isFinite(t)) return false
  return windows.some((w) => t >= w.start - 60_000 && t <= w.end + 300_000)
}

// ---------------------------------------------------------------------------
// 独立 worktree：让被委托的 Codex 只在自己的分支/目录里动手
// ---------------------------------------------------------------------------
// 本机 /usr/bin/git 是 1.8.x（无 git worktree、无 -C），现代 git 在 /opt/cloud-dev/bin。
// systemd 的 PATH 通常不含它，所以这里显式挑一个 >= 2.5 的 git。
function resolveGitBin(cfg) {
  if (cfg.gitBin) return cfg.gitBin
  const candidates = [
    '/opt/cloud-dev/bin/git',
    '/usr/local/bin/git',
    '/usr/bin/git',
    'git',
  ]
  for (const c of candidates) {
    const res = spawnSync(c, ['--version'], { encoding: 'utf8' })
    if (res.status !== 0) continue
    const m = /git version (\d+)\.(\d+)/.exec(res.stdout || '')
    if (!m) continue
    const major = Number(m[1])
    const minor = Number(m[2])
    if (major > 2 || (major === 2 && minor >= 5)) return c
  }
  return 'git'
}

// 注意：老版本 git 不支持 git -C，统一用 cwd 选项。
let GIT_BIN = 'git'

function git(cwd, args, { allowFail = false } = {}) {
  const res = spawnSync(GIT_BIN, args, { cwd, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 })
  if (res.status !== 0 && !allowFail) {
    throw new Error(`${GIT_BIN} ${args.join(' ')} 失败：${(res.stderr || res.stdout || '').trim()}`)
  }
  return { ok: res.status === 0, out: (res.stdout || '').trim(), err: (res.stderr || '').trim() }
}

function currentHead(workdir) {
  const r = git(workdir, ['rev-parse', '--abbrev-ref', 'HEAD'], { allowFail: true })
  return r.ok ? r.out : ''
}

/** 列出仍存在的 ops-fix 分支（含最新提交），避免被委托方重复排查同一问题。 */
function listOpenFixBranches(cfg, excludeBranch) {
  const r = git(cfg.workdir, ['branch', '--list', 'ops-fix/*', '--format=%(refname:short)|%(subject)'], { allowFail: true })
  if (!r.ok || !r.out) return []
  return r.out
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .map((l) => {
      const [name, ...rest] = l.split('|')
      return { name, subject: rest.join('|') }
    })
    .filter((b) => b.name && b.name !== excludeBranch)
}

/** 建 worktree，返回其路径与分支名。 */
function createWorktree(cfg, batchId, branch) {
  const dir = path.join(cfg.worktreesDir, batchId)
  fs.mkdirSync(cfg.worktreesDir, { recursive: true })
  // 复用已存在的同名 worktree（重试场景）
  if (fs.existsSync(dir)) return dir
  const base = cfg.baseRef || currentHead(cfg.workdir)
  const r = git(cfg.workdir, ['worktree', 'add', '-b', branch, dir, base], { allowFail: true })
  if (!r.ok) {
    // 分支已存在（重试/历史遗留）时改用该分支
    const r2 = git(cfg.workdir, ['worktree', 'add', dir, branch], { allowFail: true })
    if (!r2.ok) throw new Error(`创建 worktree 失败：${r.err || r2.err}`)
  }
  return dir
}

/** 保留最近 keepWorktrees 个，其余清理（不动用户手工创建的其他 worktree）。
 *  已不存在的路径视为已清理，一并从状态里剔除。 */
function pruneWorktrees(cfg, state) {
  const mine = state.worktrees || []
  const gone = []
  const alive = []
  for (const wt of mine) {
    if (fs.existsSync(wt.path)) alive.push(wt)
    else gone.push(wt.path)
  }
  const removed = [...gone]
  if (alive.length > cfg.keepWorktrees) {
    for (const wt of alive.slice(0, alive.length - cfg.keepWorktrees)) {
      git(cfg.workdir, ['worktree', 'remove', '--force', wt.path], { allowFail: true })
      removed.push(wt.path)
    }
  }
  git(cfg.workdir, ['worktree', 'prune'], { allowFail: true })
  state.worktrees = alive.filter((wt) => !removed.includes(wt.path))
  return removed
}

// PATH 里的 codex 是 codexhost shim（依赖 CODEXHOST_* 环境，systemd/cron 下不可用）。
// 这里显式定位官方 codex 二进制，保证 headless 环境也能跑。
function resolveCodexBin() {
  const candidates = [
    process.env.CODEXHOST_STOCK_CODEX_PATH,
    '/opt/node-v22.23.1-linux-x64-glibc-217/lib/node_modules/@openai/codex/node_modules/@openai/codex-linux-x64/vendor/x86_64-unknown-linux-musl/bin/codex',
    '/opt/node-v22.23.1-linux-x64-glibc-217/bin/codex',
    '/usr/local/bin/codex',
    '/usr/bin/codex',
  ].filter(Boolean)
  for (const c of candidates) {
    try { if (fs.existsSync(c)) return c } catch {}
  }
  return 'codex'
}

function safePath(current) {
  const base = '/root/.codexhost/remote/bin:/usr/local/bin:/usr/bin:/bin'
  return current ? `${current}:${base}` : base
}

function pickBackend(cfg) {
  if (cfg.backend !== 'auto') return cfg.backend
  const parent = cfg.parentThread || process.env.CODEX_THREAD_ID
  return parent ? 'codexhost' : 'codex-exec'
}

function delegate(cfg, prompt, batchId, workdir, forcedModel) {
  const backend = pickBackend(cfg)
  const model = forcedModel !== undefined ? forcedModel : cfg.model
  const targetDir = workdir || cfg.workdir
  fs.mkdirSync(cfg.reportsDir, { recursive: true })
  const requestId = `ops-auto-${batchId}`
  const promptFile = path.join(cfg.reportsDir, `${batchId}.prompt.md`)
  fs.writeFileSync(promptFile, prompt)

  if (backend === 'codexhost') {
    const parent = cfg.parentThread || process.env.CODEX_THREAD_ID
    if (!parent) throw new Error('codexhost 后端需要 --parent-thread 或 $CODEX_THREAD_ID')
    // 被委托方若直接在共享检出上改代码/切分支会污染本工作区（实测发生过），
    // 默认要求 Harness 用独立 worktree；harness 不支持时自动降级并记录。
    const args = [
      'delegate', 'start',
      '--harness', cfg.harness,
      '--parent-thread', parent,
      '--cwd', targetDir,
      '--request-id', requestId,
      '--task', prompt,
      '--format', 'compact',
    ]
    if (cfg.worktree) args.push('--worktree')
    if (model) args.push('--model', model)
    const res = spawnSync('codexhost', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
    if (res.error) throw new Error(`codexhost 调用失败：${res.error.message}`)
    if (res.status !== 0) throw new Error(`codexhost 失败（exit ${res.status}）：${(res.stderr || res.stdout || '').trim()}`)
    let parsed = {}
    try { parsed = JSON.parse(res.stdout) } catch { parsed = { raw: res.stdout } }
    return { backend, requestId, promptFile, ...parsed }
  }

  // codex-exec：headless，无需 Desktop Runtime，适合 systemd/cron。
  // 注意 PATH 上的 codex 是 codexhost shim，脱离 Desktop Runtime 时会因缺少
  // CODEXHOST_* 环境而失败，所以这里优先用官方 codex 二进制。
  const exe = cfg.codexBin || resolveCodexBin()
  const args = ['exec', '-C', targetDir, '-s', 'danger-full-access', '-']
  if (model) args.push('-m', model)
  const outFile = path.join(cfg.reportsDir, `${batchId}.last-message.md`)
  args.push('-o', outFile)
  const res = spawnSync(exe, args, {
    input: prompt,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    env: { ...process.env, PATH: safePath(process.env.PATH) },
  })
  if (res.error) throw new Error(`codex exec 调用失败（${exe}）：${res.error.message}`)
  if (res.status !== 0) throw new Error(`codex exec 失败（exit ${res.status}）：${(res.stderr || res.stdout || '').slice(-2000)}`)
  const out = res.stdout || ''
  // 网关自身故障（例如 auto 路由 503 no eligible model）会让 codex 反复重连后只输出 ERROR：
  // 此时"命令成功"但没有任何评估产出，必须识别出来，否则会被当成完成而静默丢批次。
  const failed = /ERROR: (Reconnecting\.\.\.|unexpected status)/.test(out) ||
    /unexpected status 5\d\d/.test(out)
  return { backend, requestId, promptFile, outputFile: outFile, status: failed ? 'failed' : 'completed', upstreamFailed: failed }
}

// ---------------------------------------------------------------------------
// 可选：把本批错误标记为已解决（写生产库，默认关闭）
// ---------------------------------------------------------------------------
function autoResolve(cfg, ids) {
  const sql = `UPDATE ops_error_logs SET resolved = true, resolved_at = now()
WHERE id = ANY(ARRAY[${ids.map(Number).join(',')}]::bigint)
RETURNING id;`
  const res = spawnSync('docker', [
    'exec', '-i', cfg.dbContainer,
    'psql', '-U', cfg.dbUser, '-d', cfg.dbName, '-X', '-A', '-t', '-v', 'ON_ERROR_STOP=1', '-f', '-',
  ], { input: sql, encoding: 'utf8' })
  if (res.status !== 0) throw new Error(`auto-resolve 失败：${(res.stderr || '').trim()}`)
  return (res.stdout || '').trim().split('\n').filter(Boolean).length
}

// ---------------------------------------------------------------------------
// 主流程
// ---------------------------------------------------------------------------
function main() {
  let cfg = parseArgs(process.argv.slice(2))
  if (cfg.help) { process.stdout.write(HELP); return 0 }
  cfg = loadConfig(cfg)
  GIT_BIN = resolveGitBin(cfg)

  if (cfg.source === 'sql') {
    // 首次运行不回溯历史：默认从当前最大 id 开始
    if (cfg.sinceId === undefined) {
      const s = loadState(cfg.statePath)
      if (s.lastId > 0) cfg.sinceId = s.lastId
      else {
        const res = spawnSync('docker', ['exec', '-i', cfg.dbContainer, 'psql', '-U', cfg.dbUser,
          '-d', cfg.dbName, '-X', '-A', '-t', '-c', 'SELECT COALESCE(MAX(id),0) FROM ops_error_logs;'],
        { encoding: 'utf8' })
        if (res.status !== 0) throw new Error(`读取当前最大 id 失败：${(res.stderr || '').trim()}`)
        cfg.sinceId = Number((res.stdout || '0').trim())
        process.stderr.write(`[ops-auto-delegate] 首次运行：从 id=${cfg.sinceId} 之后开始采集\n`)
      }
    }
  } else if (cfg.sinceId === undefined) {
    cfg.sinceId = loadState(cfg.statePath).lastId
  }

  const errors = cfg.source === 'sql' ? fetchFromSql(cfg, cfg.sinceId) : fetchFromApi(cfg, cfg.sinceId)
  const delegatedIds = new Set()
  const state = loadState(cfg.statePath)
  for (const d of state.delegated) for (const id of d.errorIds || []) delegatedIds.add(Number(id))

  const ignoreRes = cfg.ignorePatterns.map((p) => new RegExp(p))
  const hostIps = [...new Set([...detectHostIps(), ...cfg.hostIps])]
  const windows = collectDelegationWindows(state)
  const selfNoise = errors.filter((e) => isSelfNoise(e, cfg, hostIps, windows))
  const pending = errors
    .filter((e) => !delegatedIds.has(Number(e.id)))
    .filter((e) => !isSelfNoise(e, cfg, hostIps))
    .filter((e) => !ignoreRes.some((re) => re.test(signatureOf(e))))

  const clusters = clusterErrors(pending)
  const lastDelegatedAt = state.delegated.length
    ? new Date(state.delegated[state.delegated.length - 1].at).getTime()
    : 0
  const cooldownActive = lastDelegatedAt && (Date.now() - lastDelegatedAt) / 60000 < cfg.cooldownMinutes
  const reasons = shouldTrigger(clusters, pending, cfg)
  const batch = pending.slice(0, cfg.maxBatch)

  const decision = {
    source: cfg.source,
    sinceId: cfg.sinceId,
    scanned: errors.length,
    selfNoiseFiltered: selfNoise.length,
    pending: pending.length,
    signatures: clusters.length,
    trigger: reasons.length > 0 && !cooldownActive,
    reasons,
    cooldownActive: Boolean(cooldownActive),
    backend: pickBackend(cfg),
    delegated: false,
  }

  if (!decision.trigger) {
    if (!cooldownActive && pending.length) {
      decision.reasons = [`未达阈值（待处理 ${pending.length} 条 / 签名 ${clusters.length} 个）`]
    }
    if (!cooldownActive && !pending.length && selfNoise.length) {
      decision.reasons = [`待处理 0 条（其中 ${selfNoise.length} 条为本次委托自身的噪音，已过滤）`]
    }
    if (cooldownActive) decision.reasons = [`冷却中（距上次委托不足 ${cfg.cooldownMinutes} 分钟）`]
    if (cfg.json) process.stdout.write(JSON.stringify(decision, null, 2) + '\n')
    else process.stdout.write(`[ops-auto-delegate] 不委托：${decision.reasons.join('；')}\n`)
    return 0
  }

  const batchKey = crypto
    .createHash('sha1')
    .update(batch.map((e) => e.id).join(',') + '|' + reasons.join(';'))
    .digest('hex')
  const batchId = `${new Date().toISOString().slice(0, 10)}-${batchKey.slice(0, 12)}`
  const branch = `ops-fix/${batchKey.slice(0, 12)}`
  const base = cfg.baseRef || currentHead(cfg.workdir)
  // dry-run 不建 worktree，但要展示将来会用的路径
  const workdir = cfg.dryRun ? path.join(cfg.worktreesDir, batchId) : createWorktree(cfg, batchId, branch)
  const openBranches = listOpenFixBranches(cfg, branch)
  const prompt = buildPrompt(
    cfg,
    { errors: batch, clusters, reasons, batchKey },
    { workdir, branch, base, openBranches }
  )

  if (cfg.printPrompt) process.stdout.write(prompt + '\n\n=== END OF PROMPT ===\n\n')

  if (cfg.dryRun) {
    fs.mkdirSync(cfg.reportsDir, { recursive: true })
    const promptFile = path.join(cfg.reportsDir, `${batchId}.prompt.md`)
    fs.writeFileSync(promptFile, prompt)
    decision.batchId = batchId
    decision.errorIds = batch.map((e) => e.id)
    decision.promptFile = promptFile
    decision.dryRun = true
    if (cfg.json) process.stdout.write(JSON.stringify(decision, null, 2) + '\n')
    else {
      process.stdout.write(`[ops-auto-delegate] --dry-run：将委托 ${batch.length} 条（批次 ${batchId}）\n`)
      process.stdout.write(`[ops-auto-delegate] 触发原因：${reasons.join('；')}\n`)
      process.stdout.write(`[ops-auto-delegate] 提示词已写入 ${promptFile}（加 --print-prompt 可直接打印）\n`)
    }
    return 0
  }

  const runStartedAt = new Date().toISOString()
  const result = withLock(cfg.statePath, () => {
    const fresh = loadState(cfg.statePath)
    for (const id of batch.map((e) => e.id)) {
      if (fresh.delegated.some((d) => (d.errorIds || []).includes(id))) {
        throw new Error(`批次包含已委托错误 id=${id}，放弃本次以避免重复委托`)
      }
    }
    let res = delegate(cfg, prompt, batchId, workdir)
    if (res.status === 'failed' && cfg.model !== cfg.fallbackModel) {
      process.stderr.write(
        `[ops-auto-delegate] 委托失败（模型 ${cfg.model || 'harness 默认'} 无产出，疑似网关自身故障），` +
        `改用回退模型 ${cfg.fallbackModel || 'harness 默认'} 重试一次\n`
      )
      res = delegate(cfg, prompt, batchId, workdir, cfg.fallbackModel)
      res.retriedWithFallback = true
    }
    let resolvedCount = 0
    if (cfg.autoResolve) resolvedCount = autoResolve(cfg, batch.map((e) => e.id))
    fresh.lastId = Math.max(fresh.lastId, ...batch.map((e) => Number(e.id)))
    fresh.delegated.push({
      at: new Date().toISOString(),
      startedAt: runStartedAt,
      batchId,
      errorIds: batch.map((e) => Number(e.id)),
      signatures: clusters.map((c) => c.signature),
      thread: res.thread || null,
      backend: res.backend,
      autoResolved: resolvedCount,
      worktree: workdir,
      branch,
      base,
    })
    fresh.baseRef = fresh.baseRef || base
    fresh.worktrees = [...(fresh.worktrees || []), { path: workdir, branch, batchId, at: new Date().toISOString() }]
    const pruned = pruneWorktrees(cfg, fresh)
    saveState(cfg.statePath, fresh)
    return { ...res, resolvedCount, worktree: workdir, branch, pruned }
  })

  decision.delegated = true
  decision.batchId = batchId
  decision.errorIds = batch.map((e) => e.id)
  decision.result = result
  if (cfg.json) process.stdout.write(JSON.stringify(decision, null, 2) + '\n')
  else {
    process.stdout.write(`[ops-auto-delegate] 已委托 ${batch.length} 条错误 → ${result.thread || result.outputFile || result.backend}\n`)
    process.stdout.write(`[ops-auto-delegate] 批次ID=${batchId} 提示词=${result.promptFile}\n`)
    process.stdout.write(`[ops-auto-delegate] 独立工作区=${result.worktree}（分支 ${result.branch}）\n`)
  }
  return 0
}

try {
  process.exitCode = main()
} catch (err) {
  process.stderr.write(`[ops-auto-delegate] 错误：${err.message}\n`)
  process.exitCode = 1
}
