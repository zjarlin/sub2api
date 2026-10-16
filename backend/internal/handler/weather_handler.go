package handler

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/platform/weather"

	"github.com/gin-gonic/gin"
)

// WeatherHandler 天气服务 HTTP 处理器。
type WeatherHandler struct {
	aggregator *weather.Aggregator
}

// NewWeatherHandler 创建天气处理器。
func NewWeatherHandler(aggregator *weather.Aggregator) *WeatherHandler {
	return &WeatherHandler{aggregator: aggregator}
}

// Weather 处理统一天气查询。
// POST /api/v1/weather
func (h *WeatherHandler) Weather(c *gin.Context) {
	if h.aggregator == nil || !h.aggregator.Enabled() {
		response.Error(c, http.StatusServiceUnavailable, "Weather service is not available")
		return
	}
	var req weather.WeatherRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	result, err := h.aggregator.Fetch(c.Request.Context(), &req)
	if err != nil {
		if errors.Is(err, weather.ErrProviderUnavailable) {
			response.BadRequest(c, "Unknown or unavailable weather provider")
			return
		}
		var chain *weather.ChainError
		if errors.As(err, &chain) {
			c.JSON(http.StatusBadGateway, response.Response{
				Code: http.StatusBadGateway, Message: chain.Error(), Reason: "weather_providers_failed",
				Data: gin.H{"attempts": chain.Attempts},
			})
			return
		}
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, result)
}

// Providers 返回可用天气数据源及其能力与健康分。
// GET /api/v1/weather/providers
func (h *WeatherHandler) Providers(c *gin.Context) {
	if h.aggregator == nil {
		response.Success(c, gin.H{"providers": []weather.ProviderInfo{}})
		return
	}
	response.Success(c, gin.H{"providers": h.aggregator.Providers()})
}
