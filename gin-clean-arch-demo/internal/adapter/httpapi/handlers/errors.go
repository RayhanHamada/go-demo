package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"gin-clean-arch-demo/internal/domain"
	"gin-clean-arch-demo/internal/logger"
)

// ErrorResponse is the body of every non-2xx response. Field is present only
// on validation failures.
type ErrorResponse struct {
	Error string `json:"error" example:"email: must be a valid address"`
	Field string `json:"field,omitempty" enums:"id,email,display_name,status,limit,offset,request" example:"email"`
}

// respondError maps domain errors to protocol responses, logging the
// unexpected ones so the client only sees a generic message. The logger comes
// from the request context, so the line joins the request's other records.
func (h *Handler) respondError(c *gin.Context, err error) {
	log := logger.FromContext(c.Request.Context(), zerolog.Nop()).With().Str("layer", "http").Logger()

	switch {
	case errors.Is(err, domain.ErrInvalid):
		body := ErrorResponse{Error: err.Error()}
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			body.Field = string(ve.Field)
		}
		log.Warn().Err(err).Int("status", http.StatusBadRequest).Msg("request rejected: invalid input")
		c.AbortWithStatusJSON(http.StatusBadRequest, body)
	case errors.Is(err, domain.ErrNotFound):
		log.Info().Err(err).Int("status", http.StatusNotFound).Msg("request rejected: not found")
		c.AbortWithStatusJSON(http.StatusNotFound, ErrorResponse{Error: err.Error()})
	case errors.Is(err, domain.ErrEmailTaken):
		log.Warn().Err(err).Int("status", http.StatusConflict).Msg("request rejected: conflict")
		c.AbortWithStatusJSON(http.StatusConflict, ErrorResponse{Error: err.Error()})
	case errors.Is(err, domain.ErrStateConflict):
		log.Warn().Err(err).Int("status", http.StatusConflict).Msg("request rejected: state conflict")
		c.AbortWithStatusJSON(http.StatusConflict, ErrorResponse{Error: err.Error()})
	case errors.Is(err, domain.ErrVersionConflict):
		log.Warn().Err(err).Int("status", http.StatusPreconditionFailed).Msg("request rejected: stale version")
		c.AbortWithStatusJSON(http.StatusPreconditionFailed, ErrorResponse{Error: err.Error()})
	case errors.Is(err, domain.ErrCustomerNotFound):
		log.Info().Err(err).Int("status", http.StatusNotFound).Msg("request rejected: unknown customer")
		c.AbortWithStatusJSON(http.StatusNotFound, ErrorResponse{Error: err.Error()})
	default:
		log.Error().Err(err).Int("status", http.StatusInternalServerError).Msg("request failed: unexpected error")
		c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
	}
}
