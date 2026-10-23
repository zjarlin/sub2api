#!/usr/bin/env bash
# =============================================================================
# 内置适配器「平台注册表」——部署脚本与天津/252 共用。
# =============================================================================
# 单一数据源：新增平台只需在这里加一行，不需要再改 compose 或流水线。
#
# 每行字段（用 | 分隔）：
#   1) PROFILE       compose profile 名（= 开关变量后缀小写）
#   2) SWITCH        .env 中的开关变量名（=1 启用）
#   3) ADAPTER_KEY   共享密钥变量名（首次部署自动生成并回写 .env）
#   4) URL_VAR       平台地址变量的基准名（BUILTIN_ADAPTER_<X>_URL）。
#                    启用时导出 BUILTIN_ADAPTER_<X>_URL=<CONTAINER_URL>；
#                    无论是否启用，都导出 ADAPTER_<X>_API_SOURCE（启用=地址，未启用=空串），
#                    由 compose 直接读 SOURCE，避免 ${VAR:-} 从 .env 继承残留值。
#   5) CONTAINER_URL 适配器在容器网络内的地址
#   6) SERVICE       compose 服务名（用于 up/depends 校验），"-" 表示无本地容器
# =============================================================================
ADAPTER_REGISTRY=(
  "desktop|SUB2API_DESKTOP|DESKTOP_ADAPTER_KEY|BUILTIN_ADAPTER_DESKTOP_URL|http://sub2api-desktop:8080|sub2api-desktop"
  "traework|SUB2API_TRAEWORK|TRAEWORK_ADAPTER_KEY|BUILTIN_ADAPTER_TRAEWORK_URL|http://sub2api-traework:7864|sub2api-traework"
  "workbuddy|SUB2API_WORKBUDDY|WORKBUDDY_ADAPTER_KEY|BUILTIN_ADAPTER_WORKBUDDY_URL|http://sub2api-workbuddy:7863|sub2api-workbuddy"
  "vibex|SUB2API_VIBEX|VIBEX_ADAPTER_KEY|BUILTIN_ADAPTER_VIBEX_URL|http://sub2api-vibex:7866|sub2api-vibex"
  "zcode|SUB2API_ZCODE|ZCODE_ADAPTER_KEY|BUILTIN_ADAPTER_ZCODE_URL|http://sub2api-zcode:7865|sub2api-zcode"
  "deepseek-web|SUB2API_DEEPSEEK_WEB|DEEPSEEK_WEB_ADAPTER_KEY|BUILTIN_ADAPTER_DEEPSEEK_WEB_URL|http://sub2api-deepseek-web:7867|sub2api-deepseek-web"
  "madao|SUB2API_MADAO|MADAO_ADAPTER_KEY|BUILTIN_ADAPTER_MADAO_URL|http://sub2api-madao:7870|sub2api-madao"
  "gemini|SUB2API_GEMINI|GEMINI_ADAPTER_KEY|BUILTIN_ADAPTER_GEMINI_URL|http://sub2api-gemini:7877|sub2api-gemini"
  "cursor|SUB2API_CURSOR|CURSOR_ADAPTER_KEY|BUILTIN_ADAPTER_CURSOR_URL|http://sub2api-cursor:7868|sub2api-cursor"
  "windsurf|SUB2API_WINDSURF|WINDSURF_ADAPTER_KEY|BUILTIN_ADAPTER_WINDSURF_URL|http://sub2api-windsurf:7869|sub2api-windsurf"
  "teleagent|SUB2API_TELEAGENT|TELEAGENT_ADAPTER_KEY|BUILTIN_ADAPTER_TELEAGENT_URL|http://sub2api-teleagent:7872|sub2api-teleagent"
  "arena|SUB2API_ARENA|ARENA_AGENT_BRIDGE_KEY|BUILTIN_ADAPTER_ARENA_URL|http://sub2api-arena:7867|sub2api-arena"
  # ChatGPT 网页版：不是后端内置平台，账号是普通 OpenAI API Key（base_url 指向适配器），
  # 因此不注入 BUILTIN_ADAPTER_*，只需容器在网内且密钥与账号一致。
  "chatgptweb|SUB2API_CHATGPTWEB|CHATGPT_WEB_ADAPTER_KEY|-|http://sub2api-chatgptweb:7881|sub2api-chatgptweb"
  # 纯外部服务：没有本地容器，只注入地址；地址写在 .env（未配置则视为关闭）。
  "laya|SUB2API_LAYA|LAYA_API_KEY|BUILTIN_ADAPTER_LAYA_URL|http://edge-laya:18082|-"
  "jev|SUB2API_JEV|JEV_ADAPTER_KEY|BUILTIN_ADAPTER_JEV_URL|https://api.commandcode.ai/provider/v1|-"
)

# read_env_flag <VAR_NAME> <env_file>
# 只读 .env 的字面量，不执行其中 shell 内容。
read_env_flag() {
  local name="$1" file="$2" result=""
  [ -f "$file" ] || { printf '0'; return; }
  result="$(awk -F= -v key="$name" '
    $1 ~ "^[[:space:]]*(export[[:space:]]+)?" key "[[:space:]]*$" {
      value=$2; sub(/#.*/, "", value); gsub(/[[:space:]"\047]/, "", value); result=value
    }
    END { if (result == "1") print "1"; else print "0" }
  ' "$file")"
  printf '%s' "$result"
}

# read_env_value <VAR_NAME> <env_file>：返回字面量值（空表示未设置）。
read_env_value() {
  local name="$1" file="$2" result=""
  [ -f "$file" ] || { printf ''; return; }
  result="$(awk -F= -v key="$name" '
    $1 ~ "^[[:space:]]*(export[[:space:]]+)?" key "[[:space:]]*$" {
      value=substr($0, index($0, "=")+1); sub(/#.*/, "", value); gsub(/[[:space:]"\047]/, "", value); result=value
    }
    END { printf "%s", result }
  ' "$file")"
  printf '%s' "$result"
}

# ensure_env_key <VAR_NAME> <env_file> <backup_dir>
# 首次部署时为已启用平台生成共享密钥并持久化，后续重建复用。
ensure_env_key() {
  local name="$1" file="$2" backup_dir="${3:-}"
  [ -n "${!name:-}" ] && return 0
  local present
  present="$(read_env_value "$name" "$file")"
  if [ -z "$present" ]; then
    [ -n "$backup_dir" ] && { mkdir -p "$backup_dir"; (umask 077; cp "$file" "$backup_dir/env.before-$name"); }
    local new_key
    new_key="$(openssl rand -hex 32)"
    printf '\n%s=%s\n' "$name" "$new_key" >> "$file"
  fi
  unset "$name"
}

# adapter_source_var <URL_VAR>
# BUILTIN_ADAPTER_TRAEWORK_URL -> ADAPTER_TRAEWORK_API_SOURCE
# 未启用的平台也必须导出（空串），否则 compose 里 ${ADAPTER_X_API_SOURCE:-}
# 会退回从 .env / 宿主环境继承，残留值会让主服务误以为平台可用。
adapter_source_var() {
  local name="${1#BUILTIN_ADAPTER_}"
  name="${name%_URL}"
  printf 'ADAPTER_%s_API_SOURCE' "$name"
}

# adapter_key_var_for_url_var <BUILTIN_ADAPTER_X_URL>
# 返回该平台共享密钥在 .env 里的变量名（如 WORKBUDDY_ADAPTER_KEY）。
# 候选容器用 docker run 起，不经过 compose 的 ${VAR} 替换，需要按平台把
# 密钥映射成 BUILTIN_ADAPTER_X_KEY 显式传入，这里提供唯一的对应关系。
adapter_key_var_for_url_var() {
  local target="$1" entry profile switch key_var url_var url service
  for entry in "${ADAPTER_REGISTRY[@]}"; do
    IFS='|' read -r profile switch key_var url_var url service <<< "$entry"
    if [ "$url_var" = "$target" ]; then
      printf '%s' "$key_var"
      return 0
    fi
  done
  return 1
}

# resolve_enabled_adapters <env_file>
# 计算：本次要启动的 profile 列表、要注入的地址变量、要生成的密钥变量。
# 结果通过全局变量返回：
#   ADAPTER_PROFILES    启用的 profile 名
#   ADAPTER_URL_EXPORTS 每个平台的 `ADAPTER_<X>_API_SOURCE=<地址或空>`（全部平台）
#   ADAPTER_URLVARS     启用的平台对应的 `BUILTIN_ADAPTER_<X>_URL=<地址>`
#   ADAPTER_KEY_VARS    启用平台的共享密钥变量名
#   ADAPTER_SERVICES    启用平台在目标机上的 compose 服务名
ADAPTER_PROFILES=()
ADAPTER_URL_EXPORTS=()
ADAPTER_URLVARS=()
ADAPTER_KEY_VARS=()
# 仅在目标机有本地容器时才收集的服务名（用于精确 up，不连带 postgres/redis）。
ADAPTER_SERVICES=()
resolve_enabled_adapters() {
  local file="$1" entry profile switch key_var url_var url service source_var enabled
  ADAPTER_PROFILES=(); ADAPTER_URL_EXPORTS=(); ADAPTER_URLVARS=(); ADAPTER_KEY_VARS=(); ADAPTER_SERVICES=()
  local legacy=""
  legacy="${SUB2API_BUILTIN_ADAPTERS:-}"
  [ -z "$legacy" ] && legacy="$(read_env_flag SUB2API_BUILTIN_ADAPTERS "$file")"
  for entry in "${ADAPTER_REGISTRY[@]}"; do
    IFS='|' read -r profile switch key_var url_var url service <<< "$entry"
    # 显式环境变量优先于 .env 字面量。
    local on="${!switch:-}"
    [ -z "$on" ] && on="$(read_env_flag "$switch" "$file")"
    # 兼容旧开关：SUB2API_BUILTIN_ADAPTERS=1 等价于启用这五个本地容器适配器。
    if [ "$on" != "1" ] && [ "$legacy" = "1" ]; then
      case "$switch" in
        SUB2API_DESKTOP|SUB2API_TRAEWORK|SUB2API_WORKBUDDY|SUB2API_VIBEX|SUB2API_ZCODE) on="1" ;;
      esac
    fi
    enabled=0
    if [ "$on" = "1" ]; then
      # 外部服务（service = -）用 .env 里配置的地址；其余用容器内固定地址。
      if [ "$service" = "-" ] && [ "$url_var" != "-" ]; then
        url="$(read_env_value "$url_var" "$file")"
        [ -z "$url" ] && on="0"   # 未配置地址 => 视为关闭
      fi
      [ "$on" = "1" ] && enabled=1
    fi
    if [ "$enabled" = 1 ]; then
      ADAPTER_PROFILES+=("$profile")
      ADAPTER_KEY_VARS+=("$key_var")
      [ "$url_var" != "-" ] && ADAPTER_URLVARS+=("$url_var=$url")
      [ "$service" != "-" ] && ADAPTER_SERVICES+=("$service")
    fi
    # 逐平台导出 SOURCE：启用给地址，未启用给空串（覆盖 .env 残留）。
    if [ "$url_var" != "-" ]; then
      source_var="$(adapter_source_var "$url_var")"
      if [ "$enabled" = 1 ]; then
        ADAPTER_URL_EXPORTS+=("$source_var=$url")
      else
        ADAPTER_URL_EXPORTS+=("$source_var=")
      fi
    fi
  done
}

# adapter_profiles_csv：把 ADAPTER_PROFILES 拼成 COMPOSE_PROFILES 需要的逗号串。
adapter_profiles_csv() {
  local IFS=,
  # 空数组在 set -u 下展开会报错，先兜底。
  printf '%s' "${ADAPTER_PROFILES[*]:-}"
}
