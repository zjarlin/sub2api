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
  model: '',
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
  --model <m>               透传给 Codex 的模型（默认用 harness 原生默认）
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

function loadState(statePath) {
  if (!fs.existsSync(statePath)) return { lastId: 0, delegated: [] }
  const s = JSON.parse(fs.readFileSync(statePath, 'utf8'))
  return { lastId: s.lastId || 0, delegated: Array.isArray(s.delegated) ? s.delegated : [] }
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
  const res = spawnSync('curl', ['-sS', '-H', `x-api-key: ${key}`,
    '-H', `x-api-key: ${key}`, url.toString()], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
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

function buildPrompt(cfg, batch) {
  const { errors, clusters, reasons, batchKey } = batch
  const batchId = batchKey.slice(0, 12)
  const lines = []
  lines.push(
    '你是资深后端工程师。运维监控已累积一批 sub2api 网关的「请求错误」日志，请对每一批错误先评估、再按需修复。',
    '',
    '【第一步：先读仓库根目录 AGENTS.md】',
    `工作目录（代码工作区）：${cfg.workdir}`,
    '',
    '【硬性禁令（来自该仓库 AGENTS.md，必须遵守）】',
    '- 禁止执行任何 `docker compose up ... sub2api`（含 -d / --no-deps / --force-recreate）与任何等价写法。',
    '- 禁止重启、重建、替换任何容器；禁止自行部署镜像或改动生产 Compose/环境变量。',
    '- 需要部署才能验证时：只在报告里给出建议与理由，等用户明确同意后走 TeamCity 流水线 `sub2api / Deploy 252 Cluster` 或 `/opt/sub2api/deploy/cluster/deploy-252.sh`。',
    '- 只读排查不受限：docker ps/logs/inspect、curl 127.0.0.1:18080/health、git、读日志。',
    '- 不要 push 到主分支；改动放在新分支 `ops-fix/<批次ID>` 上提交即可，是否合并/发布由用户决定。',
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
    '4. 在 `tools/ops-auto-delegate/reports/' + batchId + '.md` 写入结构化报告：',
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
function pickBackend(cfg) {
  if (cfg.backend !== 'auto') return cfg.backend
  const parent = cfg.parentThread || process.env.CODEX_THREAD_ID
  return parent ? 'codexhost' : 'codex-exec'
}

function delegate(cfg, prompt, batchId) {
  const backend = pickBackend(cfg)
  fs.mkdirSync(cfg.reportsDir, { recursive: true })
  const requestId = `ops-auto-${batchId}`
  const promptFile = path.join(cfg.reportsDir, `${batchId}.prompt.md`)
  fs.writeFileSync(promptFile, prompt)

  if (backend === 'codexhost') {
    const parent = cfg.parentThread || process.env.CODEX_THREAD_ID
    if (!parent) throw new Error('codexhost 后端需要 --parent-thread 或 $CODEX_THREAD_ID')
    const args = [
      'delegate', 'start',
      '--harness', cfg.harness,
      '--parent-thread', parent,
      '--cwd', cfg.workdir,
      '--request-id', requestId,
      '--task', prompt,
      '--format', 'compact',
    ]
    if (cfg.model) args.push('--model', cfg.model)
    const res = spawnSync('codexhost', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
    if (res.error) throw new Error(`codexhost 调用失败：${res.error.message}`)
    if (res.status !== 0) throw new Error(`codexhost 失败（exit ${res.status}）：${(res.stderr || res.stdout || '').trim()}`)
    let parsed = {}
    try { parsed = JSON.parse(res.stdout) } catch { parsed = { raw: res.stdout } }
    return { backend, requestId, promptFile, ...parsed }
  }

  // codex-exec：headless，无需 Desktop Runtime，适合 systemd/cron
  const args = ['exec', '-C', cfg.workdir, '-s', 'danger-full-access', '-']
  if (cfg.model) args.push('-m', cfg.model)
  const outFile = path.join(cfg.reportsDir, `${batchId}.last-message.md`)
  args.push('-o', outFile)
  const res = spawnSync('codex', args, {
    input: prompt,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    env: process.env,
  })
  if (res.error) throw new Error(`codex exec 调用失败：${res.error.message}`)
  if (res.status !== 0) throw new Error(`codex exec 失败（exit ${res.status}）：${(res.stderr || res.stdout || '').slice(-2000)}`)
  return { backend, requestId, promptFile, outputFile: outFile, status: 'completed' }
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
  const pending = errors
    .filter((e) => !delegatedIds.has(Number(e.id)))
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
    pending: pending.length,
    signatures: clusters.length,
    trigger: reasons.length > 0 && !cooldownActive,
    reasons,
    cooldownActive: Boolean(cooldownActive),
    backend: pickBackend(cfg),
    delegated: false,
  }

  if (!decision.trigger) {
    if (!cooldownActive && pending.length) decision.reasons = [`未达阈值（待处理 ${pending.length} 条 / 签名 ${clusters.length} 个）`]
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
  const prompt = buildPrompt(cfg, { errors: batch, clusters, reasons, batchKey })

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

  const result = withLock(cfg.statePath, () => {
    const fresh = loadState(cfg.statePath)
    for (const id of batch.map((e) => e.id)) {
      if (fresh.delegated.some((d) => (d.errorIds || []).includes(id))) {
        throw new Error(`批次包含已委托错误 id=${id}，放弃本次以避免重复委托`)
      }
    }
    const res = delegate(cfg, prompt, batchId)
    let resolvedCount = 0
    if (cfg.autoResolve) resolvedCount = autoResolve(cfg, batch.map((e) => e.id))
    fresh.lastId = Math.max(fresh.lastId, ...batch.map((e) => Number(e.id)))
    fresh.delegated.push({
      at: new Date().toISOString(),
      batchId,
      errorIds: batch.map((e) => Number(e.id)),
      signatures: clusters.map((c) => c.signature),
      thread: res.thread || null,
      backend: res.backend,
      autoResolved: resolvedCount,
    })
    saveState(cfg.statePath, fresh)
    return { ...res, resolvedCount }
  })

  decision.delegated = true
  decision.batchId = batchId
  decision.errorIds = batch.map((e) => e.id)
  decision.result = result
  if (cfg.json) process.stdout.write(JSON.stringify(decision, null, 2) + '\n')
  else {
    process.stdout.write(`[ops-auto-delegate] 已委托 ${batch.length} 条错误 → ${result.thread || result.outputFile || result.backend}\n`)
    process.stdout.write(`[ops-auto-delegate] 批次ID=${batchId} 提示词=${result.promptFile}\n`)
  }
  return 0
}

try {
  process.exitCode = main()
} catch (err) {
  process.stderr.write(`[ops-auto-delegate] 错误：${err.message}\n`)
  process.exitCode = 1
}
