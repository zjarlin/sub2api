# Edge Vision

离线边缘计算视觉服务。与 Sub2API 主网关同机部署，提供 YOLO 检测、实例分割、姿态估计、图像分类和 OCR，像云厂商网关一样对外暴露 REST API。

## 能力

对外统一经 Sub2API 网关（18080）访问，前缀 `/vision`：

| 端点 | 说明 | 模型 |
|------|------|------|
| `POST /vision/detect` | 目标检测，返回 bbox / 类别 / 置信度 | YOLOv8n |
| `POST /vision/segment` | 实例分割，返回 bbox + 轮廓多边形 + 面积 | YOLOv8n-seg |
| `POST /vision/pose` | 人体姿态，17 个 COCO 关键点 | YOLOv8n-pose |
| `POST /vision/classify` | 图像分类 top-k | YOLOv8n-cls |
| `POST /vision/ocr` | 中英文文字识别 | RapidOCR / PP-OCRv4 |
| `GET /vision/health` | 健康检查 | - |
| `GET /vision/docs` | Swagger UI | - |

请求均为 `multipart/form-data`，图片字段名 `image`，数值参数用表单字段传递。

## 快速开始

```bash
# 本地运行（需要 Python 3.12 与 requirements.txt 依赖）
pip install -r requirements.txt
MODEL_DIR=./models PORT=18081 python3 -m app

# 调用
curl -X POST http://127.0.0.1:18081/detect \
  -F image=@sample.jpg -F confidence_threshold=0.3
```

## 部署到 252

```bash
./scripts/deploy-252.sh
# 统一入口：http://192.168.31.252:18080/vision/health
curl http://192.168.31.252:18080/vision/health
```

不占用宿主端口；容器在 sub2api 内部网络暴露 18081，由网关 `/vision/` 路径反代。

## 离线说明

- YOLOv8 四个 ONNX 模型随镜像 `COPY models /models` 打包
- OCR 模型由 `rapidocr-onnxruntime` 包内置，无需额外下载
- 运行期无 torch / ultralytics / paddlepaddle 依赖
- 已验证 `docker run --network none` 下可正常推理

## 目录

- `app/` 服务代码，`models/` 离线模型，`docker/` 构建文件
- `compose/docker-compose.yml` 单服务编排
- `scripts/deploy-252.sh` 同步 + 构建 + 启动 + 健康检查
- `tests/test_api.py` 端到端 HTTP 测试
- `AGENTS.md` 维护者约定
