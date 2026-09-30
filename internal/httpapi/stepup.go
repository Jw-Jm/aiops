package httpapi

import (
	"net/http"

	"ops-platform/internal/auth"
)

func requireTenantAdminStepUp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, ok := auth.RequestContextFromContext(r.Context())
		if !ok {
			writeTenantError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "a verified request context is required", false, "")
			return
		}
		tx, ok := TransactionFromContext(r.Context())
		if !ok {
			writeTenantError(w, http.StatusInternalServerError, "INTERNAL", "tenant transaction is unavailable", true, request.RequestID)
			return
		}
		if _, err := auth.TouchCurrentStepUpSession(r.Context(), tx, request, []string{auth.StepUpACRLevel2}); err != nil {
			writeTenantError(w, http.StatusForbidden, "STEP_UP_REQUIRED", "a current step-up session is required", false, request.RequestID)
			return
		}
		next.ServeHTTP(w, r)
	})
}
