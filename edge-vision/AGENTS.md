# Edge Vision - 边缘计算视觉服务

## 定位

- 独立于 Sub2API 主网关的边缘计算服务，部署在同一台 252 Docker 主机上
- 提供 **离线 YOLO 目标检测 / 实例分割 / 姿态估计 / 图像分类** 和 **OCR** 能力
- 所有模型随镜像打包（或只读挂载 `/models`），运行期完全离线、无外网依赖
- 252 主机为 CPU-only（无 NVIDIA 驱动），采用 ONNX Runtime CPU 推理

## 入口与边界

- **不占用任何宿主端口**。容器只在内部网络暴露 `18081`
- 对外统一走 Sub2API 网关：`http://<host>:18080/vision/<endpoint>`
- 网关通过 `deploy/cluster/nginx.conf` 的 `location /vision/` 转交 Sub2API 鉴权与计费，再由 Go 后端代理到 `edge-vision:18081`
- edge-vision 加入 sub2api 的 Compose 网络 `sub2api_sub2api-network`
- 推理代码集中在 `edge-vision/`；鉴权与用量计费由 Sub2API 后端负责

## 目录结构

```
edge-vision/
├── AGENTS.md
├── app/
│   ├── main.py          # FastAPI 路由与启动入口
│   ├── models.py        # ONNX 会话加载 + YOLO 前/后处理
│   ├── runtime.py       # 图像解码与 ONNX 元数据解析
│   ├── schemas.py       # Pydantic 响应模型（OpenAPI 文档）
│   └── api/
│       ├── detect.py    # POST /detect
│       ├── segment.py   # POST /segment
│       ├── pose.py      # POST /pose
│       ├── classify.py  # POST /classify
│       └── ocr.py       # POST /ocr
├── models/              # 离线 ONNX 模型（随镜像 COPY）
├── docker/Dockerfile    # python:3.12-slim-bookworm 单阶段构建
├── compose/docker-compose.yml
├── scripts/deploy-252.sh
└── tests/test_api.py    # 端到端 HTTP 测试
```

## API

对外路径以 `/vision` 为前缀（网关去前缀后转发），所有请求需携带 Sub2API API Key：

| 对外路径 | 方法 | 说明 |
|------|------|------|
| `/vision/detect` | POST | YOLOv8n 目标检测，返回 bbox/class/confidence |
| `/vision/segment` | POST | YOLOv8n-seg 实例分割，返回 bbox + 轮廓多边形 + 面积 |
| `/vision/pose` | POST | YOLOv8n-pose 人体姿态，返回 17 个 COCO 关键点 |
| `/vision/classify` | POST | YOLOv8n-cls 图像分类，返回 top-k |
| `/vision/ocr` | POST | RapidOCR（PP-OCRv4 ONNX），中英文文字识别 |

请求统一为 `multipart/form-data`，字段名 `image`；数值参数用表单字段传递。

## 模型与依赖

- 检测：`models/yolov8n.onnx`
- 分割：`models/yolov8n-seg.onnx`
- 姿态：`models/yolov8n-pose.onnx`
- 分类：`models/yolov8n-cls.onnx`
- OCR：`rapidocr-onnxruntime` 包内自带 PP-OCRv4 det/rec/cls ONNX 模型
- 运行期不依赖 torch / ultralytics / paddlepaddle

### 重新导出模型

若需升级模型，在有 `ultralytics` 的机器上执行：

```bash
python3 - <<'PY'
from ultralytics import YOLO
for name in ["yolov8n", "yolov8n-seg", "yolov8n-pose", "yolov8n-cls"]:
    YOLO(f"{name}.pt").export(format="onnx", opset=12, simplify=True)
PY
# 将生成的 *.onnx 放入 edge-vision/models/
```

## 部署

```bash
# 前置：sub2api 集群已在 252 运行（提供 sub2api_sub2api-network 与 18080 网关）
# 在 252 上构建并启动（首次构建需联网拉取 Python 依赖）
./edge-vision/scripts/deploy-252.sh

# 离线迁移：本地 docker save 后，在目标机 docker load
docker save zjarlin/edge-vision:1.0.0 -o edge-vision-1.0.0.tar
./edge-vision/scripts/deploy-252.sh --load edge-vision-1.0.0.tar
```

验证（统一入口 18080）：

```bash
curl -fsS -X POST http://192.168.31.252:18080/vision/detect \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -F image=@sample.jpg -F confidence_threshold=0.3
```

## 注意事项

- 构建阶段使用清华 PyPI 镜像并带重试，避免 `files.pythonhosted.org` 超时
- `/models` 以只读卷挂载，可在不重建镜像的前提下替换 ONNX 模型并重启容器
- 单 worker 共享 ONNX 会话；如并发压力大，先调大 `EDGE_VISION_THREADS`
- 输入大小限制由反向代理控制；直连时建议在客户端侧限制上传体积
