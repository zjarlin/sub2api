import os

from laya.router import Router
import torch
import uvicorn

from .server import AVAILABLE_CHECKPOINTS, create_app


def main():
    torch.set_num_threads(max(1, int(os.environ.get("LAYA_THREADS", "4"))))
    models = {
        "english": "/models/checkpoint",
        "multilingual": "/models/checkpoint/multilingual",
    }
    router = Router(models=models, device=os.environ.get("LAYA_DEVICE", "cpu"))
    # 只 preload 已挂载的检查点：Router 会用默认模型表补齐未指定的键，
    # 其中 typed-decisions 指向 Hugging Face，离线环境加载它只会失败。
    router.preload(list(AVAILABLE_CHECKPOINTS))

    uvicorn.run(
        create_app(router),
        host="0.0.0.0",
        port=int(os.environ.get("LAYA_PORT", "18082")),
    )


if __name__ == "__main__":
    main()
