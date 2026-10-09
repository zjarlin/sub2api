// constants.go 码道 Ask 的默认模型与别名。
package main

// kernelModels 是原生 Ask 模型目录暂时不可用时使用的内置目录。
var kernelModels = []builtinModel{
	{ID: "GLM-5.2", Label: "GLM-5.2", Description: "深度推理模型，适合复杂任务与深度思考"},
	{ID: "GLM-5.1", Label: "GLM-5.1", Description: "通用编码模型"},
	{ID: "Qwen3-VL-235B", Label: "Qwen3-VL-235B", Description: "多模态理解模型"},
	{ID: "maas-glm-4.7", Label: "GLM-4.7", Description: "轻量编码模型"},
}

// modelAliases 把常见 OpenAI 风格别名映射到码道模型 ID，
// 便于下游用 gpt-4o 之类的名字直连而不必改调用方。
var modelAliases = map[string]string{
	"madao":         "GLM-5.2",
	"madao-code":    "GLM-5.2",
	"glm-5.2":       "GLM-5.2",
	"glm-5.1":       "GLM-5.1",
	"qwen3-vl-235b": "Qwen3-VL-235B",
	"maas-glm-4.7":  "maas-glm-4.7",
}
