package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// HealthResponse reports each dependency separately.
type HealthResponse struct {
	Status string `json:"status" enums:"ok,degraded" example:"ok"`
	DB     string `json:"db" enums:"ok,down" example:"ok"`
	Cache  string `json:"cache" enums:"ok,down" example:"ok"`
}

// Health handles GET /healthz, reporting each dependency separately.
//
//	@Summary		Report dependency health
//	@Description	Pings the database and the cache, reporting each separately so a partial outage is visible. Intended as a readiness probe: 503 means traffic should not be sent here.
//	@Tags			Health
//	@Produce		json
//	@Success		200	{object}	HealthResponse
//	@Failure		503	{object}	HealthResponse
//	@Router			/healthz [get]
func (h *Handler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	logger := h.logger.With().Str("layer", "http").Str("op", "health").Logger()

	resp := HealthResponse{Status: "ok", DB: "ok", Cache: "ok"}
	code := http.StatusOK
	if err := h.db.Ping(ctx); err != nil {
		logger.Warn().Err(err).Msg("database health check failed")
		resp.DB, resp.Status, code = "down", "degraded", http.StatusServiceUnavailable
	}
	if err := h.cache.Ping(ctx); err != nil {
		logger.Warn().Err(err).Msg("cache health check failed")
		resp.Cache, resp.Status, code = "down", "degraded", http.StatusServiceUnavailable
	}
	c.JSON(code, resp)
}
