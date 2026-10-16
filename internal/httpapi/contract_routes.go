package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
	"ops-platform/api"
	"ops-platform/internal/auth"
)

// Register public paths and methods from the embedded contract, while existing
// domain handlers retain authorization, validation and transaction boundaries.
func newContractRouter(handler http.Handler) (http.Handler, error) {
	raw, err := api.Schemas.ReadFile("openapi/platform-v1.yaml")
	if err != nil {
		return nil, err
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return nil, err
	}
	router := chi.NewRouter()
	for path, item := range spec.Paths {
		for method := range item {
			switch method {
			case "get", "put", "post", "delete", "options", "head", "patch", "trace":
				router.Method(strings.ToUpper(method), path, handler)
			}
		}
	}
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		actor, _ := auth.RequestContextFromContext(r.Context())
		writeTenantError(w, http.StatusNotFound, "NOT_FOUND", "endpoint is unavailable", false, actor.RequestID)
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		actor, _ := auth.RequestContextFromContext(r.Context())
		writeTenantError(w, http.StatusMethodNotAllowed, "INVALID_ARGUMENT", "method is unavailable", false, actor.RequestID)
	})
	return router, nil
}
