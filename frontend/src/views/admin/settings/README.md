# 网关与系统设置组件

此目录存放独立设置界面组件。`ModelFallbackSettings.vue` 管理按能力档位降级的开关、有序档位和精确模型 ID；`VisionFallbackSettings.vue` 管理视觉助手的有序模型、列表外候选和两级超时。两者都使用独立设置接口，不参与通用设置表单的整体提交。视觉辅助的请求、计费与监控边界见 `backend/internal/service/VISION_FALLBACK.md`，主模型降级依据见 `docs/model-fallback-tiers.md`。
