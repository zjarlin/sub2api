// =====================
// 模型列表（硬编码，与 new-api 一致）
// =====================

// OpenAI
const openaiModels = [
  'gpt-4o', 'gpt-4o-mini',
  'gpt-4.1', 'gpt-4.1-mini',
  'o1', 'o3',
  // GPT-5.2 系列
  'gpt-5.2', 'gpt-5.2-2025-12-11', 'gpt-5.2-chat-latest',
  'gpt-5.2-pro', 'gpt-5.2-pro-2025-12-11',
  // GPT-5.5 系列
  'gpt-5.5',
  // GPT-5.4 系列
  'gpt-5.4', 'gpt-5.4-mini', 'gpt-5.4-2026-03-05',
  'gpt-5-mini', 'gpt-5-nano',
  // GPT-5.3 / Codex 系列
  'gpt-5.3-codex', 'gpt-5.3-codex-spark', 'codex-auto-review',
  'gpt-4o-audio-preview', 'gpt-4o-realtime-preview',
  // GPT Image 系列
  'gpt-image-1', 'gpt-image-1.5', 'gpt-image-2'
]

const seedanceModels = [
  'seedance-2-0-260128',
  'seedance-2-0-fast-260128',
  'dreamina-seedance-2-0-mini-260615',
  'seedance-1-5-pro-251215',
  'seedance-1.0-lite',
  'seedance-1.0-pro',
  'seedance-1.0-pro-fast',
  'doubao-seedance-2-0-260128',
  'doubao-seedance-2-0-fast-260128',
  'doubao-seedance-2-0-mini-260615',
  'doubao-seedance-1-5-Pro-251215'
]

const openrouterModels = [
  '~openai/gpt-latest',
  'openai/gpt-oss-120b:free',
  'openai/gpt-5',
  'openai/gpt-4o',
  'anthropic/claude-sonnet-4.5',
  'google/gemini-2.5-flash',
  'deepseek/deepseek-chat-v3.1',
  'qwen/qwen3-coder'
]

const opencodeModels = [
  'deepseek-v4-flash-free',
  'big-pickle'
]

const opencodeGoModels = [
  'opencode-go/glm-5.1',
  'opencode-go/glm-5',
  'opencode-go/kimi-k2.7-code',
  'opencode-go/kimi-k2.6',
  'opencode-go/deepseek-v4-pro',
  'opencode-go/deepseek-v4-flash',
  'opencode-go/mimo-v2.5',
  'opencode-go/mimo-v2.5-pro'
]

const opencodeGoAnthropicModels = [
  'opencode-go/minimax-m3',
  'opencode-go/minimax-m2.7',
  'opencode-go/minimax-m2.5',
  'opencode-go/qwen3.7-max',
  'opencode-go/qwen3.7-plus',
  'opencode-go/qwen3.6-plus'
]

const doubaoWebModels = [
  'doubao',
  'doubao:doubao',
  'doubao-pro',
  'doubao:doubao-pro'
]

// Anthropic Claude
export const claudeModels = [
  'claude-3-5-sonnet-20241022', 'claude-3-5-sonnet-20240620',
  'claude-3-5-haiku-20241022',
  'claude-3-7-sonnet-20250219',
  'claude-sonnet-4-20250514-thinking', 'claude-opus-4-20250514-thinking',
  'claude-sonnet-4-20250514', 'claude-opus-4-20250514',
  'claude-opus-4-1-20250805',
  'claude-sonnet-4-5-thinking', 'claude-sonnet-4-5-20250929-thinking',
  'claude-haiku-4-5-thinking', 'claude-haiku-4-5-20251001-thinking',
  'claude-opus-4-5-thinking', 'claude-opus-4-5-20251101-thinking',
  'claude-sonnet-4-5-20250929', 'claude-haiku-4-5-20251001',
  'claude-opus-4-5-20251101',
  'claude-opus-4-6-thinking',
  'claude-opus-4-6',
  'claude-opus-4-7-thinking',
  'claude-opus-4-7',
  'claude-opus-4-8',
  'claude-sonnet-4-6-thinking',
  'claude-sonnet-4-6',
  'claude-2.1', 'claude-2.0', 'claude-instant-1.2'
]

// Google Gemini
const geminiModels = [
  // Keep in sync with backend curated Gemini lists.
  // This list is intentionally conservative (models commonly available across OAuth/API key).
  'gemini-3.1-flash-image',
  'gemini-2.5-flash-image',
  'gemini-2.0-flash',
  'gemini-2.5-flash',
  'gemini-2.5-pro',
  'gemini-3.5-flash',
  'gemini-3-flash-preview',
  'gemini-3-pro-preview'
]

const mimoModels = [
  'mimo-v2.5',
  'mimo-v2.5-pro',
  'mimo-v2.5-tts-voiceclone',
  'mimo-v2.5-tts-voicedesign',
  'mimo-v2.5-tts',
  'mimo-v2-omni',
  'mimo-v2-tts'
]

const ollamaModels = [
  'llama3.1',
  'llama3.2',
  'llama3.3',
  'qwen2.5',
  'qwen3',
  'deepseek-r1',
  'mistral',
  'gemma3',
  'phi4',
  'codellama'
]

const traeModels = [
  'gpt-4o',
  'claude-3.5-sonnet'
]

const openaiLocalProxyModels = [
  'smart',
  'pool:smart',
  'doubao',
  'doubao:doubao',
  'doubao-pro',
  'doubao:doubao-pro',
  'kimi-k2.5',
  'kimi:kimi-k2.5',
  'kimi-k2',
  'kimi:kimi-k2',
  'kimi-k2.5-thinking',
  'kimi:kimi-k2.5-thinking',
  'kimi-k2-thinking',
  'kimi:kimi-k2-thinking',
  'kimi-k2.5-search',
  'kimi:kimi-k2.5-search',
  'kimi-k2-search',
  'kimi:kimi-k2-search',
  'kimi-k2.5-thinking-search',
  'kimi:kimi-k2.5-thinking-search',
  'kimi-k2.5-search-thinking',
  'kimi:kimi-k2.5-search-thinking',
  'kimi-k2-thinking-search',
  'kimi:kimi-k2-thinking-search',
  'kimi-k2-search-thinking',
  'kimi:kimi-k2-search-thinking',
  'kimi-thinking',
  'kimi:kimi-thinking',
  'kimi-search',
  'kimi:kimi-search',
  'kimi-thinking-search',
  'kimi:kimi-thinking-search',
  'kimi-search-thinking',
  'kimi:kimi-search-thinking',
  'gpt-4o',
  'gpt-4o-mini',
  'gpt-4.1',
  'gpt-4.1-mini',
  'gpt-5.4',
  'gpt-5-mini',
  'gpt-5-nano',
  'claude-3.7-sonnet',
  'claude-sonnet-4',
  'gemini-2.5-flash',
  'gemini:gemini-2.5-flash',
  'gemini-2.5-pro',
  'gemini:gemini-2.5-pro',
  'mimo-v2.5',
  'mimo:mimo-v2.5',
  'mimo-v2.5-pro',
  'mimo:mimo-v2.5-pro',
  'mimo-v2.5-tts-voiceclone',
  'mimo:mimo-v2.5-tts-voiceclone',
  'mimo-v2.5-tts-voicedesign',
  'mimo:mimo-v2.5-tts-voicedesign',
  'mimo-v2.5-tts',
  'mimo:mimo-v2.5-tts',
  'mimo-v2-omni',
  'mimo:mimo-v2-omni',
  'mimo-v2-tts',
  'mimo:mimo-v2-tts',
  'trae:gpt-4o',
  'trae:claude-3.5-sonnet',
  'opencode/big-pickle',
  'opencode/minimax-m2.5-free',
  'opencode/nemotron-3-super-free',
  'opencode/ling-2.6-flash-free'
]

const chatgptWeb2apiModels = [
  'auto',
  'chatgpt',
  'gpt-5-5',
  'gpt-5-5-thinking',
  'gpt-5-4',
  'gpt-5-4-thinking',
  'gpt-5-mini'
]

// Antigravity 官方支持的模型（精确匹配）
// 基于官方 API 返回的模型列表，只支持 Claude 4.5+ 和 Gemini 2.5+
const antigravityModels = [
  // Claude 4.5+ 系列
  'claude-opus-4-6',
  'claude-opus-4-6-thinking',
  'claude-opus-4-7',
  'claude-opus-4-8',
  'claude-opus-4-5-thinking',
  'claude-sonnet-4-6',
  'claude-sonnet-4-5',
  'claude-sonnet-4-5-thinking',
  // Gemini 2.5 系列
  'gemini-3.1-flash-image',
  'gemini-2.5-flash-image',
  'gemini-2.5-flash',
  'gemini-2.5-flash-lite',
  'gemini-2.5-flash-thinking',
  'gemini-2.5-pro',
  // Gemini 3 系列
  'gemini-3-flash',
  'gemini-3-pro-high',
  'gemini-3-pro-low',
  // Gemini 3.1 系列
  'gemini-3.1-pro-high',
  'gemini-3.1-pro-low',
  'gemini-3-pro-image',
  // 其他
  'gpt-oss-120b-medium',
  'tab_flash_lite_preview'
]

const kiroModels = [
  'claude-opus-4-7',
  'claude-opus-4-7-thinking',
  'claude-sonnet-4-6',
  'claude-sonnet-4-6-thinking',
  'claude-haiku-4-5-20251001',
  'claude-haiku-4-5-20251001-thinking'
]

// 智谱 GLM
const zhipuModels = [
  'glm-4', 'glm-4v', 'glm-4-plus', 'glm-4-0520',
  'glm-4-air', 'glm-4-airx', 'glm-4-long', 'glm-4-flash',
  'glm-4v-plus', 'glm-4.5', 'glm-4.6',
  'glm-3-turbo', 'glm-4-alltools',
  'chatglm_turbo', 'chatglm_pro', 'chatglm_std', 'chatglm_lite',
  'cogview-3', 'cogvideo'
]

// 阿里 通义千问
const qwenModels = [
  'qwen-turbo', 'qwen-plus', 'qwen-max', 'qwen-max-longcontext', 'qwen-long',
  'qwen2-72b-instruct', 'qwen2-57b-a14b-instruct', 'qwen2-7b-instruct',
  'qwen2.5-72b-instruct', 'qwen2.5-32b-instruct', 'qwen2.5-14b-instruct',
  'qwen2.5-7b-instruct', 'qwen2.5-3b-instruct', 'qwen2.5-1.5b-instruct',
  'qwen2.5-coder-32b-instruct', 'qwen2.5-coder-14b-instruct', 'qwen2.5-coder-7b-instruct',
  'qwen3-235b-a22b',
  'qwq-32b', 'qwq-32b-preview'
]

// DeepSeek
const deepseekModels = [
  'deepseek-v4-pro', 'deepseek-v4-flash',
  'deepseek-chat', 'deepseek-reasoner', 'deepseek-coder',
  'deepseek-v3', 'deepseek-v3-0324',
  'deepseek-r1', 'deepseek-r1-0528',
  'deepseek-r1-distill-qwen-32b', 'deepseek-r1-distill-qwen-14b', 'deepseek-r1-distill-qwen-7b',
  'deepseek-r1-distill-llama-70b', 'deepseek-r1-distill-llama-8b'
]

// Mistral
const mistralModels = [
  'mistral-small-latest', 'mistral-medium-latest', 'mistral-large-latest',
  'open-mistral-7b', 'open-mixtral-8x7b', 'open-mixtral-8x22b',
  'codestral-latest', 'codestral-mamba',
  'pixtral-12b-2409', 'pixtral-large-latest'
]

// Meta Llama
const metaModels = [
  'llama-3.3-70b-instruct',
  'llama-3.2-90b-vision-instruct', 'llama-3.2-11b-vision-instruct',
  'llama-3.2-3b-instruct', 'llama-3.2-1b-instruct',
  'llama-3.1-405b-instruct', 'llama-3.1-70b-instruct', 'llama-3.1-8b-instruct',
  'llama-3-70b-instruct', 'llama-3-8b-instruct',
  'codellama-70b-instruct', 'codellama-34b-instruct', 'codellama-13b-instruct'
]

// xAI Grok
const xaiModels = [
  'grok-4', 'grok-4-0709',
  'grok-3-beta', 'grok-3-mini-beta', 'grok-3-fast-beta',
  'grok-2', 'grok-2-vision', 'grok-2-image',
  'grok-beta', 'grok-vision-beta'
]

// Cohere
const cohereModels = [
  'command-a-03-2025',
  'command-r', 'command-r-plus',
  'command-r-08-2024', 'command-r-plus-08-2024',
  'c4ai-aya-23-35b', 'c4ai-aya-23-8b',
  'command', 'command-light'
]

// Yi (01.AI)
const yiModels = [
  'yi-large', 'yi-large-turbo', 'yi-large-rag',
  'yi-medium', 'yi-medium-200k',
  'yi-spark', 'yi-vision',
  'yi-1.5-34b-chat', 'yi-1.5-9b-chat', 'yi-1.5-6b-chat'
]

// Moonshot/Kimi
const moonshotModels = [
  'moonshot-v1-8k', 'moonshot-v1-32k', 'moonshot-v1-128k',
  'kimi-latest'
]

// 字节跳动 豆包
const doubaoModels = [
  'doubao-pro-256k', 'doubao-pro-128k', 'doubao-pro-32k', 'doubao-pro-4k',
  'doubao-lite-128k', 'doubao-lite-32k', 'doubao-lite-4k',
  'doubao-vision-pro-32k', 'doubao-vision-lite-32k',
  'doubao-1.5-pro-256k', 'doubao-1.5-pro-32k', 'doubao-1.5-lite-32k',
  'doubao-1.5-pro-vision-32k', 'doubao-1.5-thinking-pro'
]

// MiniMax
const minimaxModels = [
  'abab6.5-chat', 'abab6.5s-chat', 'abab6.5s-chat-pro',
  'abab6-chat',
  'abab5.5-chat', 'abab5.5s-chat'
]

// 百度 文心
const baiduModels = [
  'ernie-4.0-8k-latest', 'ernie-4.0-8k', 'ernie-4.0-turbo-8k',
  'ernie-3.5-8k', 'ernie-3.5-128k',
  'ernie-speed-8k', 'ernie-speed-128k', 'ernie-speed-pro-128k',
  'ernie-lite-8k', 'ernie-lite-pro-128k',
  'ernie-tiny-8k'
]

// 讯飞 星火
const sparkModels = [
  'spark-desk', 'spark-desk-v1.1', 'spark-desk-v2.1',
  'spark-desk-v3.1', 'spark-desk-v3.5', 'spark-desk-v4.0',
  'spark-lite', 'spark-pro', 'spark-max', 'spark-ultra'
]

// 腾讯 混元
const hunyuanModels = [
  'hunyuan-lite', 'hunyuan-standard', 'hunyuan-standard-256k',
  'hunyuan-pro', 'hunyuan-turbo', 'hunyuan-large',
  'hunyuan-vision', 'hunyuan-code'
]

// Perplexity
const perplexityModels = [
  'sonar', 'sonar-pro', 'sonar-reasoning',
  'llama-3-sonar-small-32k-online', 'llama-3-sonar-large-32k-online',
  'llama-3-sonar-small-32k-chat', 'llama-3-sonar-large-32k-chat'
]

// 所有模型（去重）
const allModelsList = Array.from(new Set<string>([
  ...openaiModels,
  ...seedanceModels,
  ...openrouterModels,
  ...opencodeModels,
  ...opencodeGoModels,
  ...doubaoWebModels,
  ...claudeModels,
  ...geminiModels,
  ...mimoModels,
  ...ollamaModels,
  ...traeModels,
  ...openaiLocalProxyModels,
  ...chatgptWeb2apiModels,
  ...zhipuModels,
  ...qwenModels,
  ...deepseekModels,
  ...mistralModels,
  ...metaModels,
  ...xaiModels,
  ...cohereModels,
  ...yiModels,
  ...moonshotModels,
  ...doubaoModels,
  ...minimaxModels,
  ...baiduModels,
  ...sparkModels,
  ...hunyuanModels,
  ...perplexityModels
]))

// 转换为下拉选项格式
export const allModels = allModelsList.map(m => ({ value: m, label: m }))

// =====================
// 预设映射
// =====================

const anthropicPresetMappings = [
  { label: 'Sonnet 4', from: 'claude-sonnet-4-20250514', to: 'claude-sonnet-4-20250514', color: 'bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-900/30 dark:text-blue-400' },
  { label: 'Sonnet 4.5', from: 'claude-sonnet-4-5-20250929', to: 'claude-sonnet-4-5-20250929', color: 'bg-indigo-100 text-indigo-700 hover:bg-indigo-200 dark:bg-indigo-900/30 dark:text-indigo-400' },
  { label: 'Sonnet 4.6', from: 'claude-sonnet-4-6', to: 'claude-sonnet-4-6', color: 'bg-indigo-100 text-indigo-700 hover:bg-indigo-200 dark:bg-indigo-900/30 dark:text-indigo-400' },
  { label: 'Opus 4.5', from: 'claude-opus-4-5-20251101', to: 'claude-opus-4-5-20251101', color: 'bg-purple-100 text-purple-700 hover:bg-purple-200 dark:bg-purple-900/30 dark:text-purple-400' },
  { label: 'Opus 4.6', from: 'claude-opus-4-6', to: 'claude-opus-4-6', color: 'bg-purple-100 text-purple-700 hover:bg-purple-200 dark:bg-purple-900/30 dark:text-purple-400' },
  { label: 'Opus 4.7', from: 'claude-opus-4-7', to: 'claude-opus-4-7', color: 'bg-purple-100 text-purple-700 hover:bg-purple-200 dark:bg-purple-900/30 dark:text-purple-400' },
  { label: 'Opus 4.8', from: 'claude-opus-4-8', to: 'claude-opus-4-8', color: 'bg-purple-100 text-purple-700 hover:bg-purple-200 dark:bg-purple-900/30 dark:text-purple-400' },
  { label: 'Haiku 3.5', from: 'claude-3-5-haiku-20241022', to: 'claude-3-5-haiku-20241022', color: 'bg-green-100 text-green-700 hover:bg-green-200 dark:bg-green-900/30 dark:text-green-400' },
  { label: 'Haiku 4.5', from: 'claude-haiku-4-5-20251001', to: 'claude-haiku-4-5-20251001', color: 'bg-emerald-100 text-emerald-700 hover:bg-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400' },
  { label: 'Opus->Sonnet', from: 'claude-opus-4-6', to: 'claude-sonnet-4-5-20250929', color: 'bg-amber-100 text-amber-700 hover:bg-amber-200 dark:bg-amber-900/30 dark:text-amber-400' }
]

const openaiPresetMappings = [
  { label: 'GPT-4o', from: 'gpt-4o', to: 'gpt-4o', color: 'bg-green-100 text-green-700 hover:bg-green-200 dark:bg-green-900/30 dark:text-green-400' },
  { label: 'GPT-4o Mini', from: 'gpt-4o-mini', to: 'gpt-4o-mini', color: 'bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-900/30 dark:text-blue-400' },
  { label: 'GPT-4.1', from: 'gpt-4.1', to: 'gpt-4.1', color: 'bg-indigo-100 text-indigo-700 hover:bg-indigo-200 dark:bg-indigo-900/30 dark:text-indigo-400' },
  { label: 'o1', from: 'o1', to: 'o1', color: 'bg-purple-100 text-purple-700 hover:bg-purple-200 dark:bg-purple-900/30 dark:text-purple-400' },
  { label: 'o3', from: 'o3', to: 'o3', color: 'bg-emerald-100 text-emerald-700 hover:bg-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400' },
  { label: 'GPT-5.3 Codex Spark', from: 'gpt-5.3-codex-spark', to: 'gpt-5.3-codex-spark', color: 'bg-teal-100 text-teal-700 hover:bg-teal-200 dark:bg-teal-900/30 dark:text-teal-400' },
  { label: 'GPT-5.2', from: 'gpt-5.2', to: 'gpt-5.2', color: 'bg-red-100 text-red-700 hover:bg-red-200 dark:bg-red-900/30 dark:text-red-400' },
  { label: 'GPT-5.5', from: 'gpt-5.5', to: 'gpt-5.5', color: 'bg-amber-100 text-amber-700 hover:bg-amber-200 dark:bg-amber-900/30 dark:text-amber-400' },
  { label: 'GPT-5.4', from: 'gpt-5.4', to: 'gpt-5.4', color: 'bg-rose-100 text-rose-700 hover:bg-rose-200 dark:bg-rose-900/30 dark:text-rose-400' },
  { label: 'Haiku→5.4', from: 'claude-haiku-4-5-20251001', to: 'gpt-5.4', color: 'bg-emerald-100 text-emerald-700 hover:bg-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400' },
  { label: 'Opus→5.4', from: 'claude-opus-4-6', to: 'gpt-5.4', color: 'bg-purple-100 text-purple-700 hover:bg-purple-200 dark:bg-purple-900/30 dark:text-purple-400' },
  { label: 'Sonnet→5.4', from: 'claude-sonnet-4-6', to: 'gpt-5.4', color: 'bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-900/30 dark:text-blue-400' }
]

const openrouterPresetMappings = [
  { label: 'GPT Latest', from: 'gpt-latest', to: '~openai/gpt-latest', color: 'bg-emerald-100 text-emerald-700 hover:bg-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400' },
  { label: 'GPT OSS Free', from: 'gpt-oss-120b', to: 'openai/gpt-oss-120b:free', color: 'bg-cyan-100 text-cyan-700 hover:bg-cyan-200 dark:bg-cyan-900/30 dark:text-cyan-400' },
  { label: 'GPT-4o', from: 'gpt-4o', to: 'openai/gpt-4o', color: 'bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-900/30 dark:text-blue-400' },
  { label: 'Gemini Flash', from: 'gemini-2.5-flash', to: 'google/gemini-2.5-flash', color: 'bg-sky-100 text-sky-700 hover:bg-sky-200 dark:bg-sky-900/30 dark:text-sky-400' },
  { label: 'Claude Sonnet', from: 'claude-sonnet-4.5', to: 'anthropic/claude-sonnet-4.5', color: 'bg-violet-100 text-violet-700 hover:bg-violet-200 dark:bg-violet-900/30 dark:text-violet-400' }
]

const opencodePresetMappings = [
  { label: 'DeepSeek V4 Flash Free', from: 'deepseek-v4-flash-free', to: 'deepseek-v4-flash-free', color: 'bg-emerald-100 text-emerald-700 hover:bg-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400' },
  { label: 'Big Pickle', from: 'big-pickle', to: 'big-pickle', color: 'bg-slate-100 text-slate-700 hover:bg-slate-200 dark:bg-slate-800/60 dark:text-slate-300' },
  { label: 'Codex→DeepSeek Free', from: 'gpt-*', to: 'deepseek-v4-flash-free', color: 'bg-teal-100 text-teal-700 hover:bg-teal-200 dark:bg-teal-900/30 dark:text-teal-400' },
  { label: 'Claude→DeepSeek Free', from: 'claude-*', to: 'deepseek-v4-flash-free', color: 'bg-violet-100 text-violet-700 hover:bg-violet-200 dark:bg-violet-900/30 dark:text-violet-400' }
]

const opencodeGoPresetMappings = [
  { label: 'GLM-5.1', from: 'opencode-go/glm-5.1', to: 'glm-5.1', color: 'bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-900/30 dark:text-blue-400' },
  { label: 'GLM-5', from: 'opencode-go/glm-5', to: 'glm-5', color: 'bg-indigo-100 text-indigo-700 hover:bg-indigo-200 dark:bg-indigo-900/30 dark:text-indigo-400' },
  { label: 'Kimi K2.7 Code', from: 'opencode-go/kimi-k2.7-code', to: 'kimi-k2.7', color: 'bg-sky-100 text-sky-700 hover:bg-sky-200 dark:bg-sky-900/30 dark:text-sky-400' },
  { label: 'Kimi K2.6', from: 'opencode-go/kimi-k2.6', to: 'kimi-k2.6', color: 'bg-cyan-100 text-cyan-700 hover:bg-cyan-200 dark:bg-cyan-900/30 dark:text-cyan-400' },
  { label: 'DeepSeek V4 Pro', from: 'opencode-go/deepseek-v4-pro', to: 'deepseek-v4-pro', color: 'bg-violet-100 text-violet-700 hover:bg-violet-200 dark:bg-violet-900/30 dark:text-violet-400' },
  { label: 'DeepSeek V4 Flash', from: 'opencode-go/deepseek-v4-flash', to: 'deepseek-v4-flash', color: 'bg-emerald-100 text-emerald-700 hover:bg-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400' },
  { label: 'MiMo-V2.5', from: 'opencode-go/mimo-v2.5', to: 'mimo-v2.5', color: 'bg-orange-100 text-orange-700 hover:bg-orange-200 dark:bg-orange-900/30 dark:text-orange-400' },
  { label: 'MiMo-V2.5-Pro', from: 'opencode-go/mimo-v2.5-pro', to: 'mimo-v2.5-pro', color: 'bg-amber-100 text-amber-700 hover:bg-amber-200 dark:bg-amber-900/30 dark:text-amber-400' }
]

const opencodeGoAnthropicPresetMappings = [
  { label: 'MiniMax M3', from: 'opencode-go/minimax-m3', to: 'minimax-m3', color: 'bg-rose-100 text-rose-700 hover:bg-rose-200 dark:bg-rose-900/30 dark:text-rose-400' },
  { label: 'MiniMax M2.7', from: 'opencode-go/minimax-m2.7', to: 'minimax-m2.7', color: 'bg-pink-100 text-pink-700 hover:bg-pink-200 dark:bg-pink-900/30 dark:text-pink-400' },
  { label: 'MiniMax M2.5', from: 'opencode-go/minimax-m2.5', to: 'minimax-m2.5', color: 'bg-fuchsia-100 text-fuchsia-700 hover:bg-fuchsia-200 dark:bg-fuchsia-900/30 dark:text-fuchsia-400' },
  { label: 'Qwen3.7 Max', from: 'opencode-go/qwen3.7-max', to: 'qwen3.7-max', color: 'bg-teal-100 text-teal-700 hover:bg-teal-200 dark:bg-teal-900/30 dark:text-teal-400' },
  { label: 'Qwen3.7 Plus', from: 'opencode-go/qwen3.7-plus', to: 'qwen3.7-plus', color: 'bg-lime-100 text-lime-700 hover:bg-lime-200 dark:bg-lime-900/30 dark:text-lime-400' },
  { label: 'Qwen3.6 Plus', from: 'opencode-go/qwen3.6-plus', to: 'qwen3.6-plus', color: 'bg-green-100 text-green-700 hover:bg-green-200 dark:bg-green-900/30 dark:text-green-400' },
  { label: 'Claude→MiniMax M3', from: 'claude-*', to: 'minimax-m3', color: 'bg-violet-100 text-violet-700 hover:bg-violet-200 dark:bg-violet-900/30 dark:text-violet-400' }
]

const doubaoWebPresetMappings = [
  { label: 'Doubao', from: 'doubao', to: 'doubao', color: 'bg-violet-100 text-violet-700 hover:bg-violet-200 dark:bg-violet-900/30 dark:text-violet-400' },
  { label: 'Doubao Prefix', from: 'doubao:doubao', to: 'doubao', color: 'bg-indigo-100 text-indigo-700 hover:bg-indigo-200 dark:bg-indigo-900/30 dark:text-indigo-400' },
  { label: 'Doubao Pro', from: 'doubao-pro', to: 'doubao-pro', color: 'bg-cyan-100 text-cyan-700 hover:bg-cyan-200 dark:bg-cyan-900/30 dark:text-cyan-400' },
  { label: 'Doubao Pro Prefix', from: 'doubao:doubao-pro', to: 'doubao-pro', color: 'bg-sky-100 text-sky-700 hover:bg-sky-200 dark:bg-sky-900/30 dark:text-sky-400' }
]

const deepseekPresetMappings = [
  { label: 'V4 Pro', from: 'gpt-5.4', to: 'deepseek-v4-pro', color: 'bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-900/30 dark:text-blue-400' },
  { label: 'V4 Flash', from: 'gpt-5.4', to: 'deepseek-v4-flash', color: 'bg-cyan-100 text-cyan-700 hover:bg-cyan-200 dark:bg-cyan-900/30 dark:text-cyan-400' },
  { label: 'Reasoner Compat', from: 'deepseek-reasoner', to: 'deepseek-v4-flash', color: 'bg-indigo-100 text-indigo-700 hover:bg-indigo-200 dark:bg-indigo-900/30 dark:text-indigo-400' },
  { label: 'Chat Compat', from: 'deepseek-chat', to: 'deepseek-v4-flash', color: 'bg-violet-100 text-violet-700 hover:bg-violet-200 dark:bg-violet-900/30 dark:text-violet-400' }
]

const mimoPresetMappings = [
  { label: 'Mimo 2.5', from: 'mimo-v2.5', to: 'mimo-v2.5', color: 'bg-rose-100 text-rose-700 hover:bg-rose-200 dark:bg-rose-900/30 dark:text-rose-400' },
  { label: 'Mimo 2.5 Pro', from: 'mimo-v2.5-pro', to: 'mimo-v2.5-pro', color: 'bg-pink-100 text-pink-700 hover:bg-pink-200 dark:bg-pink-900/30 dark:text-pink-400' },
  { label: 'Mimo Omni', from: 'mimo-v2-omni', to: 'mimo-v2-omni', color: 'bg-fuchsia-100 text-fuchsia-700 hover:bg-fuchsia-200 dark:bg-fuchsia-900/30 dark:text-fuchsia-400' },
  { label: 'Voice Clone', from: 'mimo-v2.5-tts-voiceclone', to: 'mimo-v2.5-tts-voiceclone', color: 'bg-orange-100 text-orange-700 hover:bg-orange-200 dark:bg-orange-900/30 dark:text-orange-400' },
  { label: 'TTS', from: 'mimo-v2.5-tts', to: 'mimo-v2.5-tts', color: 'bg-amber-100 text-amber-700 hover:bg-amber-200 dark:bg-amber-900/30 dark:text-amber-400' }
]

const ollamaPresetMappings = [
  { label: 'Llama 3.1', from: 'llama3.1', to: 'llama3.1', color: 'bg-stone-100 text-stone-700 hover:bg-stone-200 dark:bg-stone-800/60 dark:text-stone-300' },
  { label: 'Llama 3.2', from: 'llama3.2', to: 'llama3.2', color: 'bg-zinc-100 text-zinc-700 hover:bg-zinc-200 dark:bg-zinc-800/60 dark:text-zinc-300' },
  { label: 'Qwen 2.5', from: 'qwen2.5', to: 'qwen2.5', color: 'bg-cyan-100 text-cyan-700 hover:bg-cyan-200 dark:bg-cyan-900/30 dark:text-cyan-400' },
  { label: 'DeepSeek R1', from: 'deepseek-r1', to: 'deepseek-r1', color: 'bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-900/30 dark:text-blue-400' },
  { label: 'Mistral', from: 'mistral', to: 'mistral', color: 'bg-orange-100 text-orange-700 hover:bg-orange-200 dark:bg-orange-900/30 dark:text-orange-400' }
]

const traePresetMappings = [
  { label: 'GPT-4o', from: 'gpt-4o', to: 'gpt-4o', color: 'bg-emerald-100 text-emerald-700 hover:bg-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400' },
  { label: 'Claude 3.5 Sonnet', from: 'claude-3.5-sonnet', to: 'claude-3.5-sonnet', color: 'bg-cyan-100 text-cyan-700 hover:bg-cyan-200 dark:bg-cyan-900/30 dark:text-cyan-400' }
]

const openaiLocalProxyPresetMappings = [
  { label: 'Smart Pool', from: 'smart', to: 'pool:smart', color: 'bg-emerald-100 text-emerald-700 hover:bg-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400' },
  { label: 'Smart GPT-4o', from: 'gpt-4o', to: 'pool:smart', color: 'bg-green-100 text-green-700 hover:bg-green-200 dark:bg-green-900/30 dark:text-green-400' },
  { label: 'Smart GPT-4.1', from: 'gpt-4.1', to: 'pool:smart', color: 'bg-lime-100 text-lime-700 hover:bg-lime-200 dark:bg-lime-900/30 dark:text-lime-400' },
  { label: 'Smart GPT-5.4', from: 'gpt-5.4', to: 'pool:smart', color: 'bg-teal-100 text-teal-700 hover:bg-teal-200 dark:bg-teal-900/30 dark:text-teal-400' },
  { label: 'Kimi K2.5', from: 'kimi-k2.5', to: 'kimi:kimi-k2.5', color: 'bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-900/30 dark:text-blue-400' },
  { label: 'Doubao', from: 'doubao', to: 'doubao:doubao', color: 'bg-violet-100 text-violet-700 hover:bg-violet-200 dark:bg-violet-900/30 dark:text-violet-400' },
  { label: 'Gemini 2.5 Flash', from: 'gemini-2.5-flash', to: 'gemini:gemini-2.5-flash', color: 'bg-sky-100 text-sky-700 hover:bg-sky-200 dark:bg-sky-900/30 dark:text-sky-400' },
  { label: 'Mimo 2.5', from: 'mimo-v2.5', to: 'mimo:mimo-v2.5', color: 'bg-rose-100 text-rose-700 hover:bg-rose-200 dark:bg-rose-900/30 dark:text-rose-400' },
  { label: 'Opencode Free', from: 'opencode/big-pickle', to: 'opencode/big-pickle', color: 'bg-slate-100 text-slate-700 hover:bg-slate-200 dark:bg-slate-800/60 dark:text-slate-300' }
]

const chatgptWeb2apiPresetMappings = [
  { label: 'Auto', from: 'auto', to: 'auto', color: 'bg-emerald-100 text-emerald-700 hover:bg-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400' },
  { label: 'ChatGPT', from: 'chatgpt', to: 'auto', color: 'bg-teal-100 text-teal-700 hover:bg-teal-200 dark:bg-teal-900/30 dark:text-teal-400' },
  { label: 'GPT->Auto', from: 'gpt-*', to: 'auto', color: 'bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-900/30 dark:text-blue-400' },
  { label: 'Claude->Auto', from: 'claude-*', to: 'auto', color: 'bg-violet-100 text-violet-700 hover:bg-violet-200 dark:bg-violet-900/30 dark:text-violet-400' },
  { label: 'GPT-5.5', from: 'gpt-5-5', to: 'gpt-5-5', color: 'bg-amber-100 text-amber-700 hover:bg-amber-200 dark:bg-amber-900/30 dark:text-amber-400' },
  { label: 'GPT-5.4', from: 'gpt-5-4', to: 'gpt-5-4', color: 'bg-rose-100 text-rose-700 hover:bg-rose-200 dark:bg-rose-900/30 dark:text-rose-400' }
]

const geminiPresetMappings = [
  { label: 'Flash 2.0', from: 'gemini-2.0-flash', to: 'gemini-2.0-flash', color: 'bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-900/30 dark:text-blue-400' },
  { label: '2.5 Flash', from: 'gemini-2.5-flash', to: 'gemini-2.5-flash', color: 'bg-indigo-100 text-indigo-700 hover:bg-indigo-200 dark:bg-indigo-900/30 dark:text-indigo-400' },
  { label: '2.5 Image', from: 'gemini-2.5-flash-image', to: 'gemini-2.5-flash-image', color: 'bg-sky-100 text-sky-700 hover:bg-sky-200 dark:bg-sky-900/30 dark:text-sky-400' },
  { label: '2.5 Pro', from: 'gemini-2.5-pro', to: 'gemini-2.5-pro', color: 'bg-purple-100 text-purple-700 hover:bg-purple-200 dark:bg-purple-900/30 dark:text-purple-400' },
  { label: '3.5 Flash', from: 'gemini-3.5-flash', to: 'gemini-3.5-flash', color: 'bg-violet-100 text-violet-700 hover:bg-violet-200 dark:bg-violet-900/30 dark:text-violet-400' },
  { label: '3.1 Image', from: 'gemini-3.1-flash-image', to: 'gemini-3.1-flash-image', color: 'bg-sky-100 text-sky-700 hover:bg-sky-200 dark:bg-sky-900/30 dark:text-sky-400' }
]

// Antigravity 预设映射（支持通配符）
const antigravityPresetMappings = [
  // Claude 通配符映射
  { label: 'Claude→Sonnet', from: 'claude-*', to: 'claude-sonnet-4-5', color: 'bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-900/30 dark:text-blue-400' },
  { label: 'Sonnet→Sonnet', from: 'claude-sonnet-*', to: 'claude-sonnet-4-5', color: 'bg-indigo-100 text-indigo-700 hover:bg-indigo-200 dark:bg-indigo-900/30 dark:text-indigo-400' },
  { label: 'Opus→Opus', from: 'claude-opus-*', to: 'claude-opus-4-6-thinking', color: 'bg-purple-100 text-purple-700 hover:bg-purple-200 dark:bg-purple-900/30 dark:text-purple-400' },
  { label: 'Haiku→Sonnet', from: 'claude-haiku-*', to: 'claude-sonnet-4-5', color: 'bg-emerald-100 text-emerald-700 hover:bg-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-400' },
  { label: 'Sonnet4→4.6', from: 'claude-sonnet-4-20250514', to: 'claude-sonnet-4-6', color: 'bg-sky-100 text-sky-700 hover:bg-sky-200 dark:bg-sky-900/30 dark:text-sky-400' },
  { label: 'Sonnet4.5→4.6', from: 'claude-sonnet-4-5-20250929', to: 'claude-sonnet-4-6', color: 'bg-cyan-100 text-cyan-700 hover:bg-cyan-200 dark:bg-cyan-900/30 dark:text-cyan-400' },
  { label: 'Sonnet3.5→4.6', from: 'claude-3-5-sonnet-20241022', to: 'claude-sonnet-4-6', color: 'bg-teal-100 text-teal-700 hover:bg-teal-200 dark:bg-teal-900/30 dark:text-teal-400' },
  { label: 'Opus4.5→4.6', from: 'claude-opus-4-5-20251101', to: 'claude-opus-4-6-thinking', color: 'bg-violet-100 text-violet-700 hover:bg-violet-200 dark:bg-violet-900/30 dark:text-violet-400' },
  // Gemini 3→3.1 映射
  { label: '3-Pro-Preview→3.1-Pro-High', from: 'gemini-3-pro-preview', to: 'gemini-3.1-pro-high', color: 'bg-amber-100 text-amber-700 hover:bg-amber-200 dark:bg-amber-900/30 dark:text-amber-400' },
  { label: '3-Pro-High→3.1-Pro-High', from: 'gemini-3-pro-high', to: 'gemini-3.1-pro-high', color: 'bg-orange-100 text-orange-700 hover:bg-orange-200 dark:bg-orange-900/30 dark:text-orange-400' },
  { label: '3-Pro-Low→3.1-Pro-Low', from: 'gemini-3-pro-low', to: 'gemini-3.1-pro-low', color: 'bg-yellow-100 text-yellow-700 hover:bg-yellow-200 dark:bg-yellow-900/30 dark:text-yellow-400' },
  { label: '3.1-Pro-High透传', from: 'gemini-3.1-pro-high', to: 'gemini-3.1-pro-high', color: 'bg-orange-100 text-orange-700 hover:bg-orange-200 dark:bg-orange-900/30 dark:text-orange-400' },
  { label: '3.1-Pro-Low透传', from: 'gemini-3.1-pro-low', to: 'gemini-3.1-pro-low', color: 'bg-yellow-100 text-yellow-700 hover:bg-yellow-200 dark:bg-yellow-900/30 dark:text-yellow-400' },
  // Gemini 通配符映射
  { label: 'Gemini 3→Flash', from: 'gemini-3*', to: 'gemini-3-flash', color: 'bg-yellow-100 text-yellow-700 hover:bg-yellow-200 dark:bg-yellow-900/30 dark:text-yellow-400' },
  { label: 'Gemini 2.5→Flash', from: 'gemini-2.5*', to: 'gemini-2.5-flash', color: 'bg-orange-100 text-orange-700 hover:bg-orange-200 dark:bg-orange-900/30 dark:text-orange-400' },
  { label: '2.5-Flash-Image透传', from: 'gemini-2.5-flash-image', to: 'gemini-2.5-flash-image', color: 'bg-sky-100 text-sky-700 hover:bg-sky-200 dark:bg-sky-900/30 dark:text-sky-400' },
  { label: '3.1-Flash-Image透传', from: 'gemini-3.1-flash-image', to: 'gemini-3.1-flash-image', color: 'bg-sky-100 text-sky-700 hover:bg-sky-200 dark:bg-sky-900/30 dark:text-sky-400' },
  { label: '3-Pro-Image→3.1', from: 'gemini-3-pro-image', to: 'gemini-3.1-flash-image', color: 'bg-sky-100 text-sky-700 hover:bg-sky-200 dark:bg-sky-900/30 dark:text-sky-400' },
  { label: '3-Flash透传', from: 'gemini-3-flash', to: 'gemini-3-flash', color: 'bg-lime-100 text-lime-700 hover:bg-lime-200 dark:bg-lime-900/30 dark:text-lime-400' },
  { label: '2.5-Flash-Lite透传', from: 'gemini-2.5-flash-lite', to: 'gemini-2.5-flash-lite', color: 'bg-green-100 text-green-700 hover:bg-green-200 dark:bg-green-900/30 dark:text-green-400' },
  // 精确映射
  { label: 'Sonnet 4.6', from: 'claude-sonnet-4-6', to: 'claude-sonnet-4-6', color: 'bg-cyan-100 text-cyan-700 hover:bg-cyan-200 dark:bg-cyan-900/30 dark:text-cyan-400' },
  { label: 'Sonnet 4.5', from: 'claude-sonnet-4-5', to: 'claude-sonnet-4-5', color: 'bg-cyan-100 text-cyan-700 hover:bg-cyan-200 dark:bg-cyan-900/30 dark:text-cyan-400' },
  { label: 'Opus 4.6', from: 'claude-opus-4-6', to: 'claude-opus-4-6-thinking', color: 'bg-pink-100 text-pink-700 hover:bg-pink-200 dark:bg-pink-900/30 dark:text-pink-400' },
  { label: 'Opus 4.6-thinking', from: 'claude-opus-4-6-thinking', to: 'claude-opus-4-6-thinking', color: 'bg-pink-100 text-pink-700 hover:bg-pink-200 dark:bg-pink-900/30 dark:text-pink-400' },
  { label: 'Opus 4.7', from: 'claude-opus-4-7', to: 'claude-opus-4-7', color: 'bg-pink-100 text-pink-700 hover:bg-pink-200 dark:bg-pink-900/30 dark:text-pink-400' },
  { label: 'Opus 4.8', from: 'claude-opus-4-8', to: 'claude-opus-4-8', color: 'bg-pink-100 text-pink-700 hover:bg-pink-200 dark:bg-pink-900/30 dark:text-pink-400' }
]

const kiroPresetMappings = [
  { label: 'Opus 4.7', from: 'claude-opus-4-7', to: 'claude-opus-4.7', color: 'bg-yellow-100 text-yellow-700 hover:bg-yellow-200 dark:bg-yellow-900/30 dark:text-yellow-300' },
  { label: 'Opus 4.7 Thinking', from: 'claude-opus-4-7-thinking', to: 'claude-opus-4.7', color: 'bg-yellow-100 text-yellow-700 hover:bg-yellow-200 dark:bg-yellow-900/30 dark:text-yellow-300' },
  { label: 'Sonnet 4.6', from: 'claude-sonnet-4-6', to: 'claude-sonnet-4.6', color: 'bg-orange-100 text-orange-700 hover:bg-orange-200 dark:bg-orange-900/30 dark:text-orange-300' },
  { label: 'Sonnet 4.6 Thinking', from: 'claude-sonnet-4-6-thinking', to: 'claude-sonnet-4.6', color: 'bg-orange-100 text-orange-700 hover:bg-orange-200 dark:bg-orange-900/30 dark:text-orange-300' },
  { label: 'Haiku 4.5', from: 'claude-haiku-4-5-20251001', to: 'claude-haiku-4.5', color: 'bg-green-100 text-green-700 hover:bg-green-200 dark:bg-green-900/30 dark:text-green-300' },
  { label: 'Haiku 4.5 Thinking', from: 'claude-haiku-4-5-20251001-thinking', to: 'claude-haiku-4.5', color: 'bg-green-100 text-green-700 hover:bg-green-200 dark:bg-green-900/30 dark:text-green-300' }
]

// Bedrock 预设映射（与后端 DefaultBedrockModelMapping 保持一致）
const bedrockPresetMappings = [
  { label: 'Opus 4.6', from: 'claude-opus-4-6', to: 'us.anthropic.claude-opus-4-6-v1', color: 'bg-pink-100 text-pink-700 hover:bg-pink-200 dark:bg-pink-900/30 dark:text-pink-400' },
  { label: 'Opus 4.7', from: 'claude-opus-4-7', to: 'us.anthropic.claude-opus-4-7-v1', color: 'bg-pink-100 text-pink-700 hover:bg-pink-200 dark:bg-pink-900/30 dark:text-pink-400' },
  { label: 'Opus 4.8', from: 'claude-opus-4-8', to: 'us.anthropic.claude-opus-4-8-v1', color: 'bg-pink-100 text-pink-700 hover:bg-pink-200 dark:bg-pink-900/30 dark:text-pink-400' },
  { label: 'Sonnet 4.6', from: 'claude-sonnet-4-6', to: 'us.anthropic.claude-sonnet-4-6', color: 'bg-cyan-100 text-cyan-700 hover:bg-cyan-200 dark:bg-cyan-900/30 dark:text-cyan-400' },
  { label: 'Opus 4.5', from: 'claude-opus-4-5-thinking', to: 'us.anthropic.claude-opus-4-5-20251101-v1:0', color: 'bg-pink-100 text-pink-700 hover:bg-pink-200 dark:bg-pink-900/30 dark:text-pink-400' },
  { label: 'Sonnet 4.5', from: 'claude-sonnet-4-5', to: 'us.anthropic.claude-sonnet-4-5-20250929-v1:0', color: 'bg-cyan-100 text-cyan-700 hover:bg-cyan-200 dark:bg-cyan-900/30 dark:text-cyan-400' },
  { label: 'Haiku 4.5', from: 'claude-haiku-4-5', to: 'us.anthropic.claude-haiku-4-5-20251001-v1:0', color: 'bg-green-100 text-green-700 hover:bg-green-200 dark:bg-green-900/30 dark:text-green-400' },
]

const kiroDefaultMappings = kiroPresetMappings.map(({ from, to }) => ({ from, to }))

// Antigravity 默认映射（从后端 API 获取，与 constants.go 保持一致）
// 使用 fetchAntigravityDefaultMappings() 异步获取
import { getAntigravityDefaultModelMapping } from '@/api/admin/accounts'

let _antigravityDefaultMappingsCache: { from: string; to: string }[] | null = null
let _kiroDefaultMappingsCache: { from: string; to: string }[] | null = null

export async function fetchAntigravityDefaultMappings(): Promise<{ from: string; to: string }[]> {
  if (_antigravityDefaultMappingsCache !== null) {
    return _antigravityDefaultMappingsCache.map(({ from, to }) => ({ from, to }))
  }
  try {
    const mapping = await getAntigravityDefaultModelMapping()
    _antigravityDefaultMappingsCache = Object.entries(mapping).map(([from, to]) => ({ from, to }))
  } catch (e) {
    console.warn('[fetchAntigravityDefaultMappings] API failed, using empty fallback', e)
    _antigravityDefaultMappingsCache = []
  }
  return _antigravityDefaultMappingsCache.map(({ from, to }) => ({ from, to }))
}

export async function fetchKiroDefaultMappings(): Promise<{ from: string; to: string }[]> {
  if (_kiroDefaultMappingsCache !== null) {
    return _kiroDefaultMappingsCache.map(({ from, to }) => ({ from, to }))
  }
  _kiroDefaultMappingsCache = kiroDefaultMappings.map(({ from, to }) => ({ from, to }))
  return _kiroDefaultMappingsCache.map(({ from, to }) => ({ from, to }))
}

// =====================
// 常用错误码
// =====================

export const commonErrorCodes = [
  { value: 401, label: 'Unauthorized' },
  { value: 403, label: 'Forbidden' },
  { value: 429, label: 'Rate Limit' },
  { value: 500, label: 'Server Error' },
  { value: 502, label: 'Bad Gateway' },
  { value: 503, label: 'Unavailable' },
  { value: 529, label: 'Overloaded' }
]

// =====================
// 辅助函数
// =====================

// 按平台获取模型
export function getModelsByPlatform(platform: string): string[] {
  switch (platform) {
    case 'openai': return openaiModels
    case 'seedance': return seedanceModels
    case 'deepseek': return deepseekModels
    case 'openrouter': return openrouterModels
    case 'opencode': return opencodeModels
    case 'opencode-go': return opencodeGoModels
    case 'opencode-go-anthropic': return opencodeGoAnthropicModels
    case 'doubao-web': return doubaoWebModels
    case 'anthropic':
    case 'claude': return claudeModels
    case 'gemini': return geminiModels
    case 'mimo': return mimoModels
    case 'ollama': return ollamaModels
    case 'trae': return traeModels
    case 'openai-local-proxy': return openaiLocalProxyModels
    case 'chatgpt-web2api': return chatgptWeb2apiModels
    case 'antigravity': return antigravityModels
    case 'kiro': return kiroModels
    case 'zhipu': return zhipuModels
    case 'qwen': return qwenModels
    case 'mistral': return mistralModels
    case 'meta': return metaModels
    case 'xai': return xaiModels
    case 'cohere': return cohereModels
    case 'yi': return yiModels
    case 'moonshot': return moonshotModels
    case 'doubao': return doubaoModels
    case 'minimax': return minimaxModels
    case 'baidu': return baiduModels
    case 'spark': return sparkModels
    case 'hunyuan': return hunyuanModels
    case 'perplexity': return perplexityModels
    default: return claudeModels
  }
}

export function getModelsByPlatforms(platforms: string[]): string[] {
  if (platforms.length === 0) {
    return [...allModelsList]
  }
  return Array.from(new Set(platforms.flatMap(platform => getModelsByPlatform(platform))))
}

// 按平台获取预设映射
export function getPresetMappingsByPlatform(platform: string) {
  if (platform === 'openai') return openaiPresetMappings
  if (platform === 'deepseek') return deepseekPresetMappings
  if (platform === 'openrouter') return openrouterPresetMappings
  if (platform === 'opencode') return opencodePresetMappings
  if (platform === 'opencode-go') return opencodeGoPresetMappings
  if (platform === 'opencode-go-anthropic') return opencodeGoAnthropicPresetMappings
  if (platform === 'doubao-web') return doubaoWebPresetMappings
  if (platform === 'gemini') return geminiPresetMappings
  if (platform === 'mimo') return mimoPresetMappings
  if (platform === 'ollama') return ollamaPresetMappings
  if (platform === 'trae') return traePresetMappings
  if (platform === 'openai-local-proxy') return openaiLocalProxyPresetMappings
  if (platform === 'chatgpt-web2api') return chatgptWeb2apiPresetMappings
  if (platform === 'antigravity') return antigravityPresetMappings
  if (platform === 'kiro') return kiroPresetMappings
  if (platform === 'bedrock') return bedrockPresetMappings
  return anthropicPresetMappings
}

// =====================
// 构建模型映射对象（用于 API）
// =====================

// isValidWildcardPattern 校验通配符格式：* 只能放在末尾
// 导出供表单组件使用实时校验
export function isValidWildcardPattern(pattern: string): boolean {
  const starIndex = pattern.indexOf('*')
  if (starIndex === -1) return true // 无通配符，有效
  // * 必须在末尾，且只能有一个
  return starIndex === pattern.length - 1 && pattern.lastIndexOf('*') === starIndex
}

export type ModelRestrictionMode = 'whitelist' | 'mapping' | 'combined'

export interface ModelMappingEntry {
  from: string
  to: string
}

export function splitModelMappingObject(
  modelMapping?: Record<string, unknown> | null
): { allowedModels: string[]; modelMappings: ModelMappingEntry[] } {
  const allowedModels: string[] = []
  const modelMappings: ModelMappingEntry[] = []

  if (!modelMapping || typeof modelMapping !== 'object') {
    return { allowedModels, modelMappings }
  }

  for (const [rawFrom, rawTo] of Object.entries(modelMapping)) {
    if (typeof rawTo !== 'string') continue
    const from = rawFrom.trim()
    const to = rawTo.trim()
    if (!from || !to) continue

    if (from === to) {
      allowedModels.push(from)
    } else {
      modelMappings.push({ from, to })
    }
  }

  return { allowedModels, modelMappings }
}

export function buildModelMappingObject(
  mode: ModelRestrictionMode,
  allowedModels: string[],
  modelMappings: ModelMappingEntry[]
): Record<string, string> | null {
  const mapping: Record<string, string> = {}

  if (mode === 'whitelist' || mode === 'combined') {
    for (const model of allowedModels) {
      const normalizedModel = model.trim()
      if (!normalizedModel) continue
      // whitelist 模式的本意是"精确模型列表"，如果用户输入了通配符（如 claude-*），
      // 写入 model_mapping 会导致 GetMappedModel() 把真实模型映射成 "claude-*"，从而转发失败。
      // 因此这里跳过包含通配符的条目。
      if (!normalizedModel.includes('*')) {
        mapping[normalizedModel] = normalizedModel
      }
    }
  }

  if (mode === 'mapping' || mode === 'combined') {
    for (const m of modelMappings) {
      const from = m.from.trim()
      const to = m.to.trim()
      if (!from || !to) continue
      // 校验通配符格式：* 只能放在末尾
      if (!isValidWildcardPattern(from)) {
        console.warn(`[buildModelMappingObject] 无效的通配符格式，跳过: ${from}`)
        continue
      }
      // to 不允许包含通配符
      if (to.includes('*')) {
        console.warn(`[buildModelMappingObject] 目标模型不能包含通配符，跳过: ${from} -> ${to}`)
        continue
      }
      mapping[from] = to
    }
  }

  return Object.keys(mapping).length > 0 ? mapping : null
}
