-- models_list_config 同时保存可选的模型级计费倍率。
COMMENT ON COLUMN groups.models_list_config IS '分组模型列表与模型级倍率配置；models 控制 /v1/models 展示，model_rate_multipliers 控制模型级计费倍率';
