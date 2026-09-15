package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/Wei-Shaw/sub2api/internal/handler/routermetrics"
	"github.com/gin-gonic/gin"
)

func (h *ChannelMonitorV2Handler) RegisterRoutingMetrics(routes *gin.RouterGroup) {
	cfg := config.RoutingMetricsFromEnvironment()
	routermetrics.Register(routes, h.service, cfg.KeySHA256, cfg.GroupID)
}
