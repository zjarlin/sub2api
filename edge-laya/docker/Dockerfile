FROM python:3.12-slim-bookworm

WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends curl && rm -rf /var/lib/apt/lists/*
ARG LAYA_REF=d211a50dd816280e01f7782668f23000664c8a32
RUN pip install --no-cache-dir "laya[serve] @ https://github.com/NandhaKishorM/laya/archive/${LAYA_REF}.tar.gz"
COPY app /app/app

ENV HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 HF_HOME=/models LAYA_PORT=18082 LAYA_DEVICE=cpu LAYA_MODELS=english,multilingual LAYA_PRELOAD=1
EXPOSE 18082
CMD ["python", "-m", "app"]
