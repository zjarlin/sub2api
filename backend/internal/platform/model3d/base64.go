package model3d

import "encoding/base64"

// base64Decode 兼容标准与 URL 安全字母表，并容忍缺失的 padding。
func base64Decode(value string) ([]byte, error) {
	value = trimBase64Whitespace(value)
	if data, err := base64.StdEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	if data, err := base64.URLEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	return base64.RawURLEncoding.DecodeString(value)
}

func trimBase64Whitespace(value string) string {
	out := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case ' ', '\n', '\r', '\t':
		default:
			out = append(out, value[i])
		}
	}
	return string(out)
}
