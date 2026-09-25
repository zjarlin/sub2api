# GPT-SoVITS 推理服务镜像，面向天津海光 DCU 机器。
#
# 在 hy-vllm 基础镜像（Ubuntu 22.04 + Python 3.10 + DTK 26.04 + 海光版
# torch 2.9）上构建，保留 DTK 自带的 torch，不安装 PyPI 的 torch/torchaudio。
ARG BASE_IMAGE=hy-vllm:latest
FROM ${BASE_IMAGE}

ENV DEBIAN_FRONTEND=noninteractive \
    PYTHONUNBUFFERED=1 \
    PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    DTKROOT=/opt/dtk \
    ROCM_PATH=/opt/dtk \
    HIP_PATH=/opt/dtk/hip \
    HYHAL_PATH=/opt/hyhal \
    PYTHONPATH=/opt/torchaudio-shim \
    LD_LIBRARY_PATH=/opt/dtk-26.04/.hyhal/rocm_smi/lib:/opt/ucx/lib:/opt/dtk/dcc/gcvm/lib:/opt/dtk/hip/lib:/opt/dtk/llvm/lib:/opt/dtk/lib:/opt/dtk/lib64:/opt/hyhal/lib:/opt/hyhal/lib64:/opt/dtk/dushmem/lib:/opt/dtk/opencl/lib:/opt/mpi/lib:/opt/hwloc/lib: \
    PATH=/opt/ucx/bin:/opt/dtk/bin:/opt/dtk/llvm/bin:/opt/dtk/hip/bin:/opt/hyhal/bin:/opt/dtk/opencl/bin:/opt/mpi/bin:/opt/hwloc/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

RUN apt-get update && apt-get install -y --no-install-recommends --no-upgrade \
      ffmpeg unzip wget ca-certificates \
      libsndfile1 sox libsox-fmt-all \
    && rm -rf /var/lib/apt/lists/*

# 天津访问 GitHub 受限，走 gh-proxy 镜像；Gitee 也可以作为备用。
ARG GOPROXY_GIT=https://gh-proxy.com/https://github.com
RUN git clone --depth 1 "${GOPROXY_GIT}/RVC-Boss/GPT-SoVITS.git" /opt/GPT-SoVITS

WORKDIR /opt/GPT-SoVITS

# 推理依赖；不安装 torch/torchaudio，避免覆盖 DTK 自带的 torch。
RUN python3 -m pip install --no-cache-dir \
      "numpy<2" scipy librosa==0.10.2 numba pytorch-lightning ffmpeg-python \
      cn2an pypinyin g2p_en jieba jieba_fast nltk inflect sentencepiece \
      "transformers>=4.51,<5" "peft<0.18.0" chardet PyYAML psutil split-lang \
      "fast_langdetect>=0.3.1" wordsegment rotary_embedding_torch g2pk2 ko_pron \
      ToJyutping python_mecab_ko fasttext-wheel fasttext-predict budoux \
      robust-downloader py3langid proces soundfile x_transformers "torchmetrics==1.5.2" \
      onnxruntime opencc pyopenjtalk-prebuilt

# DTK 镜像没有 torchaudio，PyPI 的与 DTK torch ABI 不兼容。GPT-SoVITS 只用到
# load / save / functional.resample / transforms.Resample，这里用 soundfile+librosa 实现。
COPY torchaudio-shim/torchaudio.py /opt/torchaudio-shim/torchaudio.py

# 预训练底座走 ModelScope（天津可达）。解压后目录在仓库根，需要挪到配置期望位置。
ARG MODEL_SCOPE_BASE=https://www.modelscope.cn/models/XXXXRT/GPT-SoVITS-Pretrained/resolve/master
RUN cd /tmp && \
    curl -fL --retry 4 -o pretrained_models.zip "${MODEL_SCOPE_BASE}/pretrained_models.zip" && \
    rm -rf /opt/GPT-SoVITS/GPT_SoVITS/pretrained_models && \
    unzip -q -o pretrained_models.zip -d /opt/GPT-SoVITS && \
    rm -f pretrained_models.zip && \
    if [ -d /opt/GPT-SoVITS/pretrained_models ] && [ ! -d /opt/GPT-SoVITS/GPT_SoVITS/pretrained_models ]; then \
      mv /opt/GPT-SoVITS/pretrained_models /opt/GPT-SoVITS/GPT_SoVITS/pretrained_models; \
    fi && \
    curl -fL --retry 4 -o G2PWModel.zip "${MODEL_SCOPE_BASE}/G2PWModel.zip" && \
    unzip -q -o G2PWModel.zip -d /opt/GPT-SoVITS/GPT_SoVITS/text && \
    rm -f G2PWModel.zip

# 预置 NLTK 数据。g2p_en 处理英文单词时需要 averaged_perceptron_tagger /
# cmudict，运行期首次 download 会走 raw.githubusercontent.com（天津不可达）
# 而长时间挂起。这里经 gh-proxy 直接拉取压缩包解压到标准搜索路径。
ARG NLTK_PROXY=https://gh-proxy.com/https://raw.githubusercontent.com/nltk/nltk_data/gh-pages/packages
RUN set -eux; \
    export NLTK_DATA=/usr/share/nltk_data; \
    mkdir -p "$NLTK_DATA/taggers" "$NLTK_DATA/corpora" "$NLTK_DATA/tokenizers"; \
    fetch_nltk() { curl -fL --retry 4 -o "$NLTK_DATA/$2/$1.zip" "${NLTK_PROXY}/$2/$1.zip"; }; \
    fetch_nltk averaged_perceptron_tagger taggers; \
    fetch_nltk averaged_perceptron_tagger_eng taggers; \
    fetch_nltk cmudict corpora; \
    fetch_nltk punkt tokenizers; \
    fetch_nltk punkt_tab tokenizers; \
    fetch_nltk wordnet corpora; \
    python3 -c "import nltk; nltk.data.find('taggers/averaged_perceptron_tagger.zip'); nltk.data.find('corpora/cmudict.zip'); print('nltk_data ok')"

# 固化海光 DCU 源码补丁，保证镜像可直接运行。
COPY patch-gpt-sovits.py /tmp/patch-gpt-sovits.py
RUN python3 /tmp/patch-gpt-sovits.py

RUN python3 -c "import torch, torchaudio; print('TORCH', torch.__version__, 'DCU', torch.cuda.is_available(), 'TA', torchaudio.__version__)"

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 9880
ENTRYPOINT ["/entrypoint.sh"]
