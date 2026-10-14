package main

import (
	"encoding/json"
	"strconv"
)

// jsString 把 Go 字符串转成安全的 JS 字面量，用于拼接选择器。
func jsString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// answerProbe 是 readAnswerJS 返回的结构；url/tail 仅用于调试日志。
type answerProbe struct {
	Text string `json:"text"`
	URL  string `json:"url"`
	Tail string `json:"tail"`
}

// decodeAnswerProbe 解析 readAnswerJS 返回的 {"text": "...", ...} 结构。
func decodeAnswerProbe(raw string) answerProbe {
	var probe answerProbe
	if raw == "" {
		return probe
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return answerProbe{}
	}
	return probe
}

func intFromEnv(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
