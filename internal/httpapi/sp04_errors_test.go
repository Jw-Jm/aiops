package httpapi

import (
	"net/http/httptest"
	"ops-platform/internal/contract"
	"testing"
)

func TestSP04ErrorMajorPreservesSourceIsolationFailure(t *testing.T) {
	w := httptest.NewRecorder()
	writeSP04Error(w, 503, publicSP04Code("SOURCE_SCOPE_UNVERIFIED"), "scope proof unavailable", true, "")
	if w.Code != 503 {
		t.Fatal("scope failure status changed")
	}
	if err := contract.Validate("https://ops.local/schemas/error-envelope/v2", w.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if publicSP04Code("SOURCE_SCOPE_UNVERIFIED") != "SOURCE_SCOPE_UNVERIFIED" {
		t.Fatal("scope failure disguised as absent capability")
	}
}
