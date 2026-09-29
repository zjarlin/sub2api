# 网关与系统设置组件

`AutoModelSettings.vue` 管理 Auto 自动选模黑名单，使用独立的 `/admin/settings/auto-model` 接口。未配置时默认使用 `doubao*`；支持精确 ID 和末尾 `*` 前缀规则，不区分大小写，不带命名空间的规则也检查供应商前缀后的模型名。保存空列表可以移除黑名单。规则应用于 Auto 候选、别名、映射目标和降级重试，手动指定模型不受此黑名单影响。

此目录存放独立设置界面组件。`ModelFallbackSettings.vue` 管理按能力档位降级的开关、有序档位和精确模型 ID；`VisionFallbackSettings.vue` 管理视觉助手的有序模型、列表外候选和两级超时。两者都使用独立设置接口，不参与通用设置表单的整体提交。视觉辅助的请求、计费与监控边界见 `backend/internal/service/VISION_FALLBACK.md`，主模型降级依据见 `docs/model-fallback-tiers.md`。
