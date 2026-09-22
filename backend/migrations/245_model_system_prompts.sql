-- 按模型配置追加 system 提示词。默认空配置，行为与升级前一致。
-- 管理员可在 设置 → 网关 中为 ds 等模型补充语言约束。
INSERT INTO settings (key, value, updated_at)
VALUES ('model_system_prompts', '{"entries":[]}', NOW())
ON CONFLICT (key) DO NOTHING;
