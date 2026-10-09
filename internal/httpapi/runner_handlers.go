package httpapi

import (
	"github.com/google/uuid"
	"net/http"
	"ops-platform/internal/action"
	"ops-platform/internal/auth"
	"strings"
)

type RunnerHandlers struct {
	Service action.Service
	Trust   auth.WorkloadTrust
}

func (h RunnerHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		actionError(w, action.ErrDenied, "")
		return
	}
	if _, err := auth.VerifyWorkload(auth.WithWorkloadTrust(r.Context(), h.Trust), r.TLS.PeerCertificates[0]); err != nil {
		actionError(w, action.ErrDenied, "")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 7 || parts[0] != "internal" || parts[1] != "v1" || parts[2] != "tenants" || parts[4] != "command-executions" || r.Method != "POST" {
		actionError(w, action.ErrInvalid, "")
		return
	}
	tenant, err := uuid.Parse(parts[3])
	if err != nil {
		actionError(w, action.ErrInvalid, "")
		return
	}
	id, err := uuid.Parse(parts[5])
	if err != nil {
		actionError(w, action.ErrInvalid, "")
		return
	}
	token := r.Header.Get("X-Runner-Token")
	if len(token) != 43 {
		actionError(w, action.ErrDenied, "")
		return
	}
	switch parts[6] {
	case "claim":
		var in struct {
			PublicKey string `json:"publicKey"`
		}
		if decodeAction(r, &in) != nil {
			actionError(w, action.ErrInvalid, "")
			return
		}
		out, err := h.Service.ClaimWithKey(r.Context(), tenant, id, token, in.PublicKey)
		if err != nil {
			actionError(w, err, "")
			return
		}
		writeJSON(w, 200, out)
		return
	case "output":
		var in action.OutputChunk
		if decodeAction(r, &in) != nil {
			actionError(w, action.ErrInvalid, "")
			return
		}
		if err = h.Service.AppendOutput(r.Context(), tenant, id, token, in); err != nil {
			actionError(w, err, "")
			return
		}
		writeJSON(w, 200, map[string]bool{"accepted": true})
		return
	case "finish":
		var in struct {
			ExitCode  int   `json:"exitCode"`
			FinalSeq  int64 `json:"finalSeq"`
			Truncated bool  `json:"truncated"`
		}
		if decodeAction(r, &in) != nil {
			actionError(w, action.ErrInvalid, "")
			return
		}
		if err = h.Service.Finish(r.Context(), tenant, id, token, in.ExitCode, in.FinalSeq, in.Truncated); err != nil {
			actionError(w, err, "")
			return
		}
		writeJSON(w, 200, map[string]bool{"accepted": true})
		return
	}
	actionError(w, action.ErrInvalid, "")
}
