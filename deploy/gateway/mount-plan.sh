#!/usr/bin/env bash
#
# 生成候选容器要复用的挂载参数。
#
# 为什么不直接用 `docker inspect -f '{{...}}'` 拼行再 read：
#   docker 的格式化模板在空字段处会留下空白的连续分隔符；bind 类型没有 Name
#   字段，若用 tab 当 IFS（tab 是空白字符，read 会把连续分隔符合并），字段会
#   整体错位，结果是每个 bind 挂载都被静默丢弃 —— 候选容器仍能通过健康检查，
#   切流后却丢掉了 /app/data。这里改为在 Python 里直接输出结果文件，shell 只
#   逐行读取，不再依赖字段分隔符的空白语义。
#
# write_mount_plan <mounts-json-file> <specs-out> <dests-out>
#   specs-out 每行一个 `docker run -v` 的参数（源:目标）
#   dests-out 每行一个容器内挂载目标
write_mount_plan() {
  local mounts_json="$1" specs_out="$2" dests_out="$3"
  python3 - "$mounts_json" "$specs_out" "$dests_out" <<'PY'
import json
import sys

mounts_json, specs_out, dests_out = sys.argv[1:4]
with open(mounts_json, encoding="utf-8") as handle:
    mounts = json.load(handle) or []

with open(specs_out, "w", encoding="utf-8") as specs, open(
    dests_out, "w", encoding="utf-8"
) as dests:
    for mount in mounts:
        destination = mount.get("Destination") or ""
        if not destination:
            continue
        mount_type = mount.get("Type") or ""
        name = mount.get("Name") or ""
        source = mount.get("Source") or ""
        # 只接受 bind 与 named volume；其他类型（tmpfs 等）无法用 -v 原样复现。
        if mount_type == "volume":
            spec = "%s:%s" % (name, destination)
        elif mount_type == "bind":
            spec = "%s:%s" % (source, destination)
        else:
            print(
                "skip %s mount at %s (cannot replicate with -v)" % (mount_type, destination),
                file=sys.stderr,
            )
            continue
        if not spec.rsplit(":", 1)[0]:
            print("skip mount with empty source at %s" % destination, file=sys.stderr)
            continue
        specs.write(spec + "\n")
        dests.write(destination + "\n")
PY
}
