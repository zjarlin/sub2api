package service

// supportsResponsesAPIMode reports whether a monitor provider can be checked
// through the OpenAI Responses-compatible wire format.
func supportsResponsesAPIMode(provider string) bool {
	switch provider {
	case MonitorProviderOpenAI, MonitorProviderGemini:
		return true
	default:
		return false
	}
}
