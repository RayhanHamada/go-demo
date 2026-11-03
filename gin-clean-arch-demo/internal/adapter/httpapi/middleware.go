package httpapi

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"gin-clean-arch-demo/internal/logger"
)

const headerRequestID = "X-Request-ID"

// RequestID accepts an inbound X-Request-ID or mints one, echoes it back, and
// puts it in the request context for correlation across layers.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(headerRequestID)
		if id == "" {
			id = logger.NewRequestID()
		}
		c.Header(headerRequestID, id)
		c.Request = c.Request.WithContext(logger.WithRequestID(c.Request.Context(), id))
		c.Next()
	}
}

// RequestLogger attaches a request-scoped logger to the context and emits one
// structured line per request after the handler returns. The context logger
// carries only correlation fields (request_id, method, path) so every layer
// can add its own identity instead of inheriting this one's.
func RequestLogger(base zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		reqLog := base.With().
			Str("request_id", logger.RequestID(c.Request.Context())).
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Logger()
		c.Request = c.Request.WithContext(logger.WithLogger(c.Request.Context(), reqLog))

		c.Next()

		status := c.Writer.Status()
		bytes := c.Writer.Size()
		if bytes < 0 {
			bytes = 0
		}
		httpLog := reqLog.With().Str("layer", "http").Logger()
		var event *zerolog.Event
		switch {
		case status >= 500:
			event = httpLog.Error()
		case status >= 400:
			event = httpLog.Warn()
		default:
			event = httpLog.Info()
		}
		event.
			Int("status", status).
			Int("bytes", bytes).
			Float64("duration_ms", float64(time.Since(start).Microseconds())/1000).
			Str("client_ip", c.ClientIP()).
			Msg("http request")
	}
}
