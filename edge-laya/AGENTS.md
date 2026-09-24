# Laya 本地推理

- 本目录维护 Laya 权重准备、镜像和内部运行服务；公网入口由 Sub2API 负责鉴权。
- 不把模型文件纳入 Git；部署前准备固定 revision 的完整 Hugging Face snapshot。
- 运行期强制 `HF_HUB_OFFLINE=1`，不在启动失败时临时下载权重。
- 只在内网 Compose 网络监听 18082，不暴露宿主端口。

## 两个对外面

同一进程、同一个 `Router` 提供两条通道，语义必须保持一致：

- `POST /v1/systemone` —— **Laya 原生契约**（由上游 `laya.serve` 提供）。
  与 JEV 共用 wire protocol，返回 `{model, answers, usage, routing}`。这是决策
  能力的真相来源，网关 `internal/jev_api` 只做原样透传。
- `POST /v1/chat/completions`、`POST /v1/responses` —— **OpenAI 兼容面**
  （`app/openai_compat.py`），供现有 OpenAI SDK / Codex 客户端使用。

改 `app/openai_compat.py` 时必须遵守：

- 协议转换只做包装，不改决策语义。`{state, questions, task, lang}` 原样传给
  `Router.predict`，不得二次编码或改写。
- `model` 解析与原生面一致：`laya` 和未知模型名一律回落自动选路（**不要硬编码
  `english`**），`laya-english` / `laya-multilingual` / `laya-typed-decisions`
  才强制指定检查点。
- Laya 不生成 token：`completion_tokens` 恒为 0，`total_tokens` 等于输入 token，
  不要伪造聊天用量冒充计费。
- 单次前向即得到完整结果，因此 SSE 只发一片再收尾，但**必须**发出终态
  （chat 的 `[DONE]`、responses 的 `response.completed`），否则网关会判为
  流中断。
- 新增行为要补 `tests/test_openai_compat.py`；该测试用注入的假 `predict`，
  不加载真实权重，可在无 torch 环境运行。
