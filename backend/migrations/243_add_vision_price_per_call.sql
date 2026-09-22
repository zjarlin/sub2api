-- 离线边缘计算视觉服务（edge-vision）按次计费单价。
-- 覆盖 YOLO 目标检测/实例分割/姿态估计/图像分类与 OCR。
-- 不与其他媒体单价（image/video/web search）互相回填：视觉服务是独立的本地推理成本。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS vision_price_per_call DECIMAL(20,8);

COMMENT ON COLUMN groups.vision_price_per_call IS '边缘计算视觉服务单次调用价格 (USD/次)；NULL 表示使用默认价';
