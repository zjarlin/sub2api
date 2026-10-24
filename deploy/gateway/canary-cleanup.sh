#!/usr/bin/env bash
#
# 候选容器的清理：删掉 canary 容器与挂载计划临时目录。
#
# 为什么单独成文件：
#   这段逻辑会被 EXIT trap 调用，而部署脚本是 `set -e`。历史缺陷是把
#   `[ -n "$mount_work" ] && rm -rf "$mount_work"` 直接放在 cleanup 末尾，
#   当 mount_work 已提前清空时整条命令返回 1，`set -e` 让脚本在
#   「switching live container」之前静默退出（退出码 1、无日志、线上未切换、
#   回滚分支也没走到）。因此清理函数必须显式保证任何分支都返回 0。
#
# cleanup_canary <canary-container-name> <mount-work-dir>
#   mount-work-dir 可为空（未创建或已清理）。
cleanup_canary() {
  local canary_name="$1" work_dir="${2:-}"
  if [ -n "$canary_name" ]; then
    docker rm -f "$canary_name" >/dev/null 2>&1 || true
  fi
  if [ -n "$work_dir" ]; then
    rm -rf "$work_dir" || true
  fi
  return 0
}
