import type { EdgeService } from './catalog'

export interface EdgeSkillEndpoint {
  key: string
  path: string
  method: string
  description: string
}

export interface BuildEdgeSkillOptions {
  service: EdgeService
  serviceTitle: string
  serviceDescription: string
  endpoints: EdgeSkillEndpoint[]
  endpoint: EdgeSkillEndpoint
  gatewayOrigin: string
  example: string
}

// 异步任务的创建、查询与下载路径；与后端路由保持一致，用于说明轮询方式。
const ASYNC_TASKS: Record<string, { create: string; status: string; content?: string }> = {
  dub: { create: '/media/videos/dub', status: '/media/tasks/{task_id}', content: '/media/tasks/{task_id}/content' },
  generation: { create: '/v1/contents/generations/tasks', status: '/v1/contents/generations/tasks/{task_id}' },
  model3d: { create: '/api/v1/3d/generations', status: '/api/v1/3d/tasks/{task_id}', content: '/api/v1/3d/tasks/{task_id}/assets/{index}' },
}

// Skill 目录名必须与 SKILL.md frontmatter 的 name 一致，统一小写连字符风格。
export function edgeSkillName(service: EdgeService): string {
  return `sub2api-edge-${service.key}`
}

export function edgeSkillPath(service: EdgeService): string {
  return `~/.codex/skills/${edgeSkillName(service)}/SKILL.md`
}

// 表格单元格内的竖线与换行会破坏 Markdown 表格，统一转义。
function tableCell(value: string): string {
  return value.replace(/\|/g, '\\|').replace(/\r?\n/g, ' ').trim()
}

/**
 * 生成可安装的 Codex Skill 文档（SKILL.md）。
 * 网关地址、端点与请求示例全部来自边缘服务工作台的真实契约，不维护第二套副本。
 */
export function buildEdgeSkill(options: BuildEdgeSkillOptions): string {
  const { service, serviceTitle, serviceDescription, endpoints, endpoint, gatewayOrigin, example } = options
  const name = edgeSkillName(service)
  // 全部为 GET 的服务没有请求体，不应声明 Content-Type；配音走 multipart 表单。
  const writable = endpoints.some(item => item.method !== 'GET')
  const contentType = !writable ? '' : service.key === 'dub' ? '`multipart/form-data`' : '`application/json`'
  const lines: string[] = [
    '---',
    `name: ${name}`,
    `description: ${JSON.stringify(`当用户需要通过 Sub2API 边缘计算网关调用${serviceTitle}时使用。`)}`,
    '---',
    '',
    `# Sub2API 边缘计算 · ${serviceTitle}`,
    '',
    serviceDescription,
    '',
    '## 网关',
    '',
    `- 地址：${gatewayOrigin}`,
    '- 鉴权：`Authorization: Bearer $SUB2API_KEY`，在控制台「API 密钥」创建后立即生效。',
    ...(contentType ? [`- 请求内容类型：${contentType}`] : ['- 请求体：以下端点均为查询接口，不需要请求体。']),
    '',
    '## 服务端点',
    '',
    '| 方法 | 路径 | 说明 |',
    '| --- | --- | --- |',
    ...endpoints.map(item => `| ${item.method} | \`${item.path}\` | ${tableCell(item.description)} |`),
    '',
    `## 请求示例（${endpoint.method} ${endpoint.path}）`,
    '',
    '```bash',
    example,
    '```',
    '',
    '## 调用规范',
    '',
    '1. 先把 `SUB2API_KEY` 环境变量设为你的网关 API Key；不要把它写入仓库、日志或示例文件。',
    '2. 同时检查 HTTP 状态码与响应体业务字段：部分接口即使 HTTP 200 也可能返回业务失败。',
    '3. 上游服务商凭据由管理员在控制台「适配上下文」维护，调用方只需要网关 API Key。',
    '4. 参数语义、响应示例与常见状态码见控制台本服务的「调用文档」页。',
  ]
  const asyncTask = ASYNC_TASKS[service.key]
  if (asyncTask) {
    lines.push(
      '',
      '## 异步任务',
      '',
      '创建接口只返回任务 ID，结果需要轮询：',
      '',
      `- 创建：\`POST ${asyncTask.create}\``,
      `- 查询：\`GET ${asyncTask.status}\``,
    )
    if (asyncTask.content) lines.push(`- 下载：\`GET ${asyncTask.content}\``)
    lines.push('', '轮询到 `status` 为 `succeeded` 后再下载；`failed` / `blocked` 时读取 `error` 字段。')
  }
  lines.push('')
  return lines.join('\n')
}
