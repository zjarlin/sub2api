import os

from laya.router import Router
from laya.serve import create_app
import torch
import uvicorn


def main():
    torch.set_num_threads(max(1, int(os.environ.get("LAYA_THREADS", "4"))))
    models = {
        "english": "/models/checkpoint",
        "multilingual": "/models/checkpoint/multilingual",
    }
    router = Router(models=models, device="cpu")
    router.preload(["english", "multilingual"])
    uvicorn.run(create_app(router), host="0.0.0.0", port=int(os.environ.get("LAYA_PORT", "18082")))


if __name__ == "__main__":
    main()
