package contract_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBundledKeycloakNeverUsesDatabaseBootstrapIdentity(t *testing.T) {
	out, err := exec.Command("helm", "template", "ops-dependencies", "../../deploy/charts/ops-dependencies", "--namespace", "ops-fresh-identities", "--show-only", "templates/keycloak.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v: %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "/keycloak\"") || strings.Contains(s, "name: \"ops-postgresql-auth\"") || !strings.Contains(s, "name: \"ops-keycloak-database\"") {
		t.Fatal("Keycloak still shares platform database or bootstrap credentials")
	}
	out, err = exec.Command("helm", "template", "ops-dependencies", "../../deploy/charts/ops-dependencies", "--namespace", "ops-fresh-identities", "--show-only", "templates/postgresql.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v: %s", err, out)
	}
	s = string(out)
	for _, required := range []string{"/docker-entrypoint-initdb.d", "pg_read_file", "NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS", "secretName: ops-keycloak-database"} {
		if !strings.Contains(s, required) {
			t.Fatalf("fixed first-init database duty missing: %s", required)
		}
	}
}
