package routermetrics

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type Source interface {
	ParseFilter(string, []string, []string, []int64) (service.ChannelMonitorV2Filter, error)
	RoutingModels(context.Context, service.ChannelMonitorV2Filter) (*service.ChannelMonitorV2List[service.ChannelMonitorV2ModelRow], error)
}

func Register(routes *gin.RouterGroup, source Source, tokenHash string, groupID int64) {
	configured, err := hex.DecodeString(tokenHash)
	valid := err == nil && len(configured) == sha256.Size && groupID > 0
	var mu sync.Mutex
	var cached *Snapshot
	routes.GET("/router/models/health", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if !valid {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "router metrics not configured"})
			return
		}
		parts := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		provided := sha256.Sum256([]byte(parts[1]))
		if subtle.ConstantTimeCompare(provided[:], configured) != 1 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if cached != nil && time.Since(cached.GeneratedAt) < time.Minute {
			c.JSON(http.StatusOK, cached)
			return
		}
		// Scope and window are server-owned, never accepted from query parameters.
		filter, err := source.ParseFilter("90m", nil, nil, []int64{groupID})
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		rows, err := source.RoutingModels(ctx, filter)
		if err != nil || rows == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "model metrics unavailable"})
			return
		}
		result := summarize(rows, groupID)
		cached = &result
		c.JSON(http.StatusOK, result)
	})
}

func summarize(rows *service.ChannelMonitorV2List[service.ChannelMonitorV2ModelRow], groupID int64) Snapshot {
	byID := map[string]*Model{}
	for _, row := range rows.Items {
		if row.Model == "" || row.Model == service.ChannelMonitorV2OtherModel {
			continue
		}
		m := byID[row.Model]
		if m == nil {
			m = &Model{ID: row.Model}
			byID[row.Model] = m
		}
		m.Successes += row.Metrics.SuccessRequests
		m.Failures += row.Metrics.ErrorRequests
		if row.Metrics.TTFT.AvgMs != nil && row.Metrics.TTFT.SampleCount > 0 {
			m.latencySum += *row.Metrics.TTFT.AvgMs * float64(row.Metrics.TTFT.SampleCount)
			m.latencyCount += row.Metrics.TTFT.SampleCount
		}
	}
	out := Snapshot{Scope: "group", GroupID: groupID, Window: "90m", GeneratedAt: time.Now().UTC(),
		DataThrough: rows.Coverage.DataThrough, CoverageComplete: rows.Coverage.CoverageComplete,
		MinimumSamples: 10, Models: make([]Model, 0, len(byID))}
	for _, m := range byID {
		m.Samples = m.Successes + m.Failures
		if m.Samples > 0 {
			rate := float64(m.Successes) / float64(m.Samples)
			m.SuccessRate = &rate
		}
		if m.latencyCount > 0 {
			avg := m.latencySum / float64(m.latencyCount)
			m.AverageTTFT = &avg
		}
		out.Models = append(out.Models, *m)
	}
	sort.Slice(out.Models, func(i, j int) bool { return out.Models[i].ID < out.Models[j].ID })
	return out
}
