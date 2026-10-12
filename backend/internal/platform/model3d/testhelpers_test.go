package model3d

import "io"

// readAllLimited 在测试里读取有界文件内容。
func readAllLimited(reader io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(reader, limit))
}
