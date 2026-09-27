package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	generated "ops-platform/gen/api"
)

// ErrorEnvelope is the generated public API error response contract.
type ErrorEnvelope = generated.ErrorEnvelope

// NewHandler adapts the generated server interface to the platform's Chi router.
func NewHandler(server generated.ServerInterface) http.Handler {
	return generated.HandlerFromMux(server, chi.NewRouter())
}
