import os

from laya.router import Router
from laya.serve import create_app
import torch
import uvicorn

from .openai_compat import create_openai_router


def main():
    torch.set_num_threads(max(1, int(os.environ.get("LAYA_THREADS", "4"))))
    models = {
        "english": "/models/checkpoint",
        "multilingual": "/models/checkpoint/multilingual",
    }
    router = Router(models=models, device="cpu")
    router.preload(["english", "multilingual"])

    # 原生决策接口是 /v1/systemone（由 laya.serve 提供）；OpenAI 兼容面复用同一个
    # Router，保证两条通道的 model 解析与推理语义完全一致。
    app = create_app(router)
    app.include_router(create_openai_router(router.predict))

    uvicorn.run(app, host="0.0.0.0", port=int(os.environ.get("LAYA_PORT", "18082")))


if __name__ == "__main__":
    main()
