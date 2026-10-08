package service

// DefaultMadaoModelIDs 是码道（华为云 CodeArts 代码智能体）适配器开箱可用的模型目录。
// 适配器会优先同步站内 AgentCenter 目录，这里只作为未同步时的默认候选。
func DefaultMadaoModelIDs() []string {
	return []string{"GLM-5.2", "GLM-5.1", "Qwen3-VL-235B", "maas-glm-4.7"}
}
