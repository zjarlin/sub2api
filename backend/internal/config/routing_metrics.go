package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
)

type RoutingMetrics struct {
	KeySHA256 string
	GroupID   int64
}

func RoutingMetricsFromEnvironment() RoutingMetrics {
	groupID, _ := strconv.ParseInt(os.Getenv("ROUTER_METRICS_GROUP_ID"), 10, 64)
	return RoutingMetrics{KeySHA256: os.Getenv("ROUTER_METRICS_KEY_SHA256"), GroupID: groupID}
}
func (c RoutingMetrics) Enabled() bool {
	hash, err := hex.DecodeString(c.KeySHA256)
	return err == nil && len(hash) == sha256.Size && c.GroupID > 0
}
