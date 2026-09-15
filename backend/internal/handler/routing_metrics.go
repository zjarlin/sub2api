package handler

import (
	"os"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler/routermetrics"
	"github.com/gin-gonic/gin"
)

func (h *ChannelMonitorV2Handler) RegisterRoutingMetrics(routes *gin.RouterGroup) {
	groupID, _ := strconv.ParseInt(os.Getenv("ROUTER_METRICS_GROUP_ID"), 10, 64)
	routermetrics.Register(routes, h.service, os.Getenv("ROUTER_METRICS_KEY_SHA256"), groupID)
}
