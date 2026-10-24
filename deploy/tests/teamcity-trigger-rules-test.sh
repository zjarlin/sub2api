#!/bin/sh
#
# .teamcity/settings.kts 的 VCS 触发规则格式回归测试。
#
# 2026-10-24 事故：24bf07083 / a3be4b906 把 triggerRules 写成单行冒号拼接
#   param("triggerRules", "+:.:-:.teamcity/**:-:deploy/macmini/**")
# TeamCity 按「一行一条规则」解析，于是整串被当成一条以 ".:-:.teamcity/**"
# 开头的 Ant 通配模式，匹配不到任何文件，vcsTrigger 从此静默：
# 2026-10-23 16:07 之后 182 网关与天津媒体的构建全部只能手动排队。
# 这类缺陷既不会让 Kotlin DSL 编译失败，也不会让部署脚本报错，只能靠格式断言兜住。
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
settings="$repo_root/.teamcity/settings.kts"

fail() {
  printf 'teamcity trigger rules test failed: %s\n' "$1" >&2
  exit 1
}

[ -f "$settings" ] || fail "$settings does not exist"

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/teamcity-trigger-rules.XXXXXX")
trap 'rm -rf "$tmpdir"' EXIT INT TERM

block_count=$(grep -c '"triggerRules",' "$settings" || true)
[ "$block_count" -eq 2 ] || fail "expected 2 triggerRules blocks (gateway + media), found ${block_count:-0}"

# 一次扫描完成两件事：
#   1) 值是单行字符串的写法记到 bad（硬失败）；
#   2) 每个三引号块内的正文（一行一条规则）按 Kotlin trimIndent() 去缩进后写到 rules.<n>。
awk -v badf="$tmpdir/bad" -v base="$tmpdir/rules" '
  function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
  function strip_open(s) { return trim(substr(s, 4)) }

  /"triggerRules",/ {
    rest = $0
    sub(/.*"triggerRules",/, "", rest)
    rest = trim(rest)
    # 三引号紧跟 triggerRules：同一行开块（合法），正文从下一行开始。
    if (rest ~ /^"""/) {
      after = strip_open(rest)
      if (after == "") { inside = 1; n++; rulef = base "." n; next }
      print "same-line: " rest > badf
      next
    }
    # 值不是三引号开块，就是普通字符串/表达式写法 —— 事故正是这种。
    if (rest != "") { print "same-line: " rest > badf; next }
    pending = 1
    next
  }
  pending {
    line = trim($0)
    if (line == "") next
    pending = 0
    if (line ~ /^"""/) {
      after = strip_open(line)
      if (after == "") { inside = 1; n++; rulef = base "." n; next }
      print "same-line: " line > badf
      next
    }
    print "next-line: " line > badf
    next
  }
  inside {
    line = trim($0)
    if (line == "") next
    if (line ~ /^"""/) { inside = 0; next }
    print line > rulef
  }
' "$settings"

[ ! -s "$tmpdir/bad" ] || fail "triggerRules must use a triple-quoted multi-line block, not a single-line string:
$(cat "$tmpdir/bad")"

rules_file_count=$(find "$tmpdir" -maxdepth 1 -name 'rules.*' | wc -l | tr -d ' ')
[ "$rules_file_count" -eq 2 ] || fail "expected 2 triggerRules bodies, parsed $rules_file_count"

for rules in "$tmpdir"/rules.*; do
  name=${rules##*/rules.}

  # 1) 单行拼接残留的标志：":-:" 表示两条规则被挤在同一行。
  if grep -F -- ':-:' "$rules" > /dev/null; then
    fail "triggerRules block #$name must list one rule per line, found colon-joined rules"
  fi

  # 2) 每条规则都必须以 + 或 - 开头，并且带 ":" 分隔符。
  malformed=$(awk '$0 !~ /^[+-]/ || $0 !~ /^[+-][^:]*:/' "$rules")
  [ -z "$malformed" ] || fail "triggerRules block #$name has malformed rule: $malformed"

  # 3) 首条必须是「包含全部」的 +:.；一旦写了任意 + 规则，
  #    TeamCity 就把隐含默认从「包含全部」翻成「排除全部」，缺了它会静默不触发。
  first=$(awk 'NR == 1 { print }' "$rules")
  [ "$first" = '+:.' ] || fail "triggerRules block #$name first rule must be '+:.' (include all), found '$first'"

  # 4) 排除行必须真正独立成行（单行写法会把它们吞进第一条规则）。
  grep -Fx -- '-:.teamcity/**' "$rules" > /dev/null \
    || fail "triggerRules block #$name is missing '-:.teamcity/**' on its own line"
done

printf 'teamcity trigger rules test passed\n'
