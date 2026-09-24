"""下载 Laya 推理所需权重到 /models/checkpoint。

国内网络下 Hugging Face 常不可达，因此优先走 ModelScope 上同一份权重镜像，
失败再回退 Hugging Face（可用 HF_ENDPOINT 指向镜像站）。
"""
from __future__ import annotations

import os
import sys

# 推理只用到这些文件；不下载整仓（README、assets、eval 等）。
ROOT_PATTERNS = [
    "rl_agent_config.json",
    "model.safetensors",
    "tokenizer/*",
    "encoder/*",
]
# 根目录是英文检查点，multilingual/ 是同仓的多语言检查点。
PATTERNS = ROOT_PATTERNS + [f"multilingual/{pattern}" for pattern in ROOT_PATTERNS]

REPO_ID = "convaiinnovations/laya"
DEST = "/models/checkpoint"

# ModelScope 与 Hugging Face 的 revision 体系彼此独立：ModelScope 镜像只有 master
# 分支，没有 HF 的 commit SHA。因此两条路径各有自己的 revision 变量，默认值分别
# 对应「当前镜像分支」与「固定快照」。
MODELSCOPE_REVISION = os.environ.get("MODELSCOPE_REVISION") or "master"
HF_REVISION = os.environ.get("HF_REVISION") or "main"


def from_modelscope(revision: str) -> bool:
    try:
        from modelscope import snapshot_download
    except Exception:
        return False
    print(f"尝试 ModelScope revision={revision}")
    try:
        snapshot_download(
            REPO_ID,
            revision=revision,
            local_dir=DEST,
            allow_patterns=PATTERNS,
        )
        return True
    except Exception as exc:  # noqa: BLE001 -- 回退到 Hugging Face
        print(f"ModelScope 拉取失败，回退 Hugging Face: {exc}", file=sys.stderr)
        return False


def from_huggingface(revision: str) -> None:
    print(f"尝试 Hugging Face revision={revision}")
    from huggingface_hub import snapshot_download

    snapshot_download(
        repo_id=REPO_ID,
        revision=revision,
        local_dir=DEST,
        allow_patterns=PATTERNS,
    )


def main() -> None:
    if not from_modelscope(MODELSCOPE_REVISION):
        from_huggingface(HF_REVISION)


if __name__ == "__main__":
    main()
