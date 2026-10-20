package openbao

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// initServer fakes only the two endpoints initialization depends on, so the
// recovery-material decision can be tested without a real OpenBao instance.
func initServer(t *testing.T, initialized *atomic.Bool, failInit, commitOnFail bool, keys map[string]any) *Client {
	t.Helper()
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sys/seal-status":
			writeJSON(w, map[string]any{"initialized": initialized.Load(), "sealed": true, "version": "2.7.0"})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/sys/init":
			if failInit {
				// commitOnFail models the observed failure: the server commits the
				// initialization and the response is lost. Without commit the request
				// simply failed and a retry stays legitimate.
				if commitOnFail {
					initialized.Store(true)
				}
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			initialized.Store(true)
			writeJSON(w, keys)
		default:
			http.NotFound(w, r)
		}
	}
	ca, key := makeTestCA(t)
	server, client := newPKITestServer(t, ca, key, handler)
	t.Cleanup(server.Close)
	return client
}

func TestInitializeReportsUnrecoverableWhenResponseIsLost(t *testing.T) {
	var initialized atomic.Bool
	client := initServer(t, &initialized, true, true, nil)
	recovery := filepath.Join(t.TempDir(), "openbao-recovery.json")

	err := client.Initialize(context.Background(), recovery)
	if !errors.Is(err, ErrRecoveryUnavailable) {
		t.Fatalf("error = %v, want ErrRecoveryUnavailable", err)
	}
	if _, statErr := os.Stat(recovery); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("empty recovery file left behind; a retry would be refused forever")
	}
}

func TestInitializeStaysRetryableWhenTheInstanceIsStillUninitialized(t *testing.T) {
	var initialized atomic.Bool
	client := initServer(t, &initialized, true, false, nil)
	recovery := filepath.Join(t.TempDir(), "openbao-recovery.json")

	if err := client.Initialize(context.Background(), recovery); err == nil || errors.Is(err, ErrRecoveryUnavailable) {
		t.Fatalf("error = %v, want a retryable outcome", err)
	}
	if _, statErr := os.Stat(recovery); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("empty recovery file left after a retryable failure")
	}
	initialized.Store(false)
	retry := initServer(t, &initialized, false, false, map[string]any{
		"keys_base64": []string{"share-a", "share-b", "share-c"}, "root_token": "root-token",
	})
	if err := retry.Initialize(context.Background(), recovery); err != nil {
		t.Fatalf("retry refused: %v", err)
	}
	raw, err := os.ReadFile(recovery)
	if err != nil || len(raw) == 0 {
		t.Fatalf("recovery material not written: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("recovery material is not JSON: %v", err)
	}
	if len(stored) == 0 {
		t.Fatal("recovery material empty")
	}
}

func TestInitializeDiscardsAnEmptyResidueOnlyWhenUninitialized(t *testing.T) {
	directory := t.TempDir()
	recovery := filepath.Join(directory, "openbao-recovery.json")
	if err := os.WriteFile(recovery, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var initialized atomic.Bool
	initialized.Store(true)
	blocked := initServer(t, &initialized, false, false, map[string]any{"keys_base64": []string{"a"}, "root_token": "t"})
	if err := blocked.Initialize(context.Background(), recovery); !errors.Is(err, ErrRecoveryUnavailable) {
		t.Fatalf("initialized instance accepted a second initialization: %v", err)
	}

	initialized.Store(false)
	client := initServer(t, &initialized, false, false, map[string]any{
		"keys_base64": []string{"share-a", "share-b", "share-c"}, "root_token": "root-token",
	})
	if err := client.Initialize(context.Background(), recovery); err != nil {
		t.Fatalf("empty residue blocked a clean retry: %v", err)
	}
	if info, err := os.Stat(recovery); err != nil || info.Size() == 0 {
		t.Fatalf("recovery material not written: %v", err)
	}
}
