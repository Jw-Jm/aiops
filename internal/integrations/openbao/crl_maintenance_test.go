package openbao

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestCRLMaintenanceOnlyEnablesAutomaticRebuild(t *testing.T) {
	for _, test := range []struct {
		name                         string
		automatic, disabled, ignored bool
		wantErr                      bool
		writes                       int
	}{
		{"existing", true, false, false, false, 0},
		{"enable", false, false, false, false, 1},
		{"revocation disabled", false, true, false, true, 0},
		{"write not applied", false, false, true, true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			automatic := test.automatic
			writes := 0
			ca, key := makeTestCA(t)
			server, client := newPKITestServer(t, ca, key, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/pki/config/crl" {
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if r.Method == http.MethodPost {
					writes++
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["auto_rebuild"] != true {
						t.Error("bootstrap changed unrelated CRL settings")
					}
					if !test.ignored {
						automatic = true
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				writeJSON(w, map[string]any{"data": map[string]any{"auto_rebuild": automatic, "disable": test.disabled, "expiry": "72h", "auto_rebuild_grace_period": "12h"}})
			})
			defer server.Close()
			if err := client.ensurePKICRLMaintenance(context.Background()); (err != nil) != test.wantErr {
				t.Fatal(err)
			}
			if writes != test.writes {
				t.Fatalf("writes=%d", writes)
			}
		})
	}
}
