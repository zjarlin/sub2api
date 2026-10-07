import argparse
import hashlib
from pathlib import Path

from huggingface_hub import snapshot_download


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model-dir", default="/models/Hy-MT2-1.8B")
    parser.add_argument("--endpoint", default="https://huggingface.co")
    args = parser.parse_args()
    snapshot_download(
        repo_id="tencent/Hy-MT2-1.8B",
        revision="9a341cd1b679d3efd23b46e847b01745a71ed792",
        endpoint=args.endpoint,
        local_dir=args.model_dir,
        allow_patterns=[
            "model.safetensors", "config.json", "generation_config.json",
            "tokenizer.json", "tokenizer_config.json", "special_tokens_map.json",
            "chat_template.jinja", "LICENSE.txt",
        ],
    )
    digest = hashlib.sha256()
    with (Path(args.model_dir) / "model.safetensors").open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            digest.update(chunk)
    if digest.hexdigest() != "29e9117a44c79f81857613601968ff482d8a23c2d6736a1710bba9e5ca4762e5":
        raise RuntimeError("Hy-MT2 weights failed SHA-256 validation")
    print("Hy-MT2 BF16 weights verified", flush=True)


if __name__ == "__main__":
    main()
