#!/usr/bin/env bash
# 宿主机内存最后一道保险：在 swap 打满/可用内存耗尽前，杀掉最大的 Chromium
# 根进程（渲染子进程会随根进程退出），避免整机进入 OOM -> 重启 -> 再 OOM 的死亡螺旋。
#
# 正常的内存边界应由容器的 mem_limit + memswap_limit（禁用 swap）承担；
# 本脚本只处理“已经越界、宿主即将锁死”的情况。
#
# 环境变量：
#   OOM_GUARD_INTERVAL       采样间隔秒数，默认 10
#   OOM_GUARD_MIN_AVAIL_MB   触发阈值 MemAvailable，默认 6144
#   OOM_GUARD_MAX_SWAP_PCT   触发阈值 swap 使用率，默认 85
#   OOM_GUARD_COOLDOWN       两次处置最小间隔秒数，默认 60
#   OOM_GUARD_DRY_RUN        1 = 只记录不杀进程
set -u

INTERVAL="${OOM_GUARD_INTERVAL:-10}"
MIN_AVAIL_MB="${OOM_GUARD_MIN_AVAIL_MB:-6144}"
MAX_SWAP_PCT="${OOM_GUARD_MAX_SWAP_PCT:-85}"
COOLDOWN="${OOM_GUARD_COOLDOWN:-60}"
DRY_RUN="${OOM_GUARD_DRY_RUN:-0}"
LAST_ACTION=0

log() {
  logger -t oom-guard -- "$*" 2>/dev/null || true
  printf '%s %s\n' "$(date '+%F %T')" "$*"
}

# 从 /proc/<pid>/cgroup 解析 Docker 容器名；解析失败时返回空。
container_of() {
  local pid=$1 id name
  id=$(awk -F/ '/:memory:/ && /\/docker\// {sub(/^.*\/docker\//, ""); sub(/\.scope$/, ""); print; exit}' "/proc/$pid/cgroup" 2>/dev/null)
  [ -z "$id" ] && return 1
  name=$(timeout 5 docker inspect --format '{{.Name}}' "$id" 2>/dev/null | sed 's#^/##')
  [ -n "$name" ] || return 1
  printf '%s\n' "$name"
}

# 选 RSS 最大的 chromium 根进程：父进程本身不是 chromium 的才算根。
pick_victim() {
  ps -eo pid,ppid,rss,comm --no-headers 2>/dev/null | awk '
    $4 ~ /^chrom/ { rss[$1]=$3; ppid[$1]=$2; seen[$1]=1 }
    END {
      best=0; bestpid=0
      for (p in seen) {
        if (ppid[p] in seen) continue
        if (rss[p] > best) { best=rss[p]; bestpid=p }
      }
      if (bestpid) printf "%s %s\n", bestpid, best
    }'
}

while :; do
  read -r avail_kb swap_total_kb swap_free_kb < <(awk '
    /^MemAvailable:/ {a=$2}
    /^SwapTotal:/    {t=$2}
    /^SwapFree:/     {f=$2}
    END {printf "%d %d %d\n", a+0, t+0, f+0}' /proc/meminfo)

  avail_mb=$((avail_kb / 1024))
  if [ "$swap_total_kb" -gt 0 ]; then
    swap_pct=$(( (swap_total_kb - swap_free_kb) * 100 / swap_total_kb ))
  else
    swap_pct=0
  fi

  if [ "$avail_mb" -lt "$MIN_AVAIL_MB" ] || \
     { [ "$swap_pct" -ge "$MAX_SWAP_PCT" ] && [ "$avail_mb" -lt $((MIN_AVAIL_MB * 2)) ]; }; then
    now=$(date +%s)
    if [ $((now - LAST_ACTION)) -ge "$COOLDOWN" ]; then
      victim=$(pick_victim)
      if [ -n "$victim" ]; then
        pid=${victim%% *}
        rss_mb=$(( ${victim##* } / 1024 ))
        name=$(container_of "$pid" || true)
        target="chromium pid=$pid"
        [ -n "$name" ] && target="container $name (chromium pid=$pid)"
        if [ "$DRY_RUN" = "1" ]; then
          log "DRY_RUN would restart $target rss=${rss_mb}MB (avail=${avail_mb}MB swap=${swap_pct}%)"
        else
          log "restarting $target rss=${rss_mb}MB (avail=${avail_mb}MB swap=${swap_pct}%)"
          if [ -n "$name" ]; then
            timeout 10 docker kill "$name" >/dev/null 2>&1 || kill -9 "$pid" 2>/dev/null || true
          else
            kill -9 "$pid" 2>/dev/null || true
          fi
        fi
      else
        log "memory pressure (avail=${avail_mb}MB swap=${swap_pct}%) but no chromium root found"
      fi
      LAST_ACTION=$now
    fi
  fi
  sleep "$INTERVAL"
done
