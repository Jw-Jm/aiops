package contract

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPlatformDatabaseCredentialsAreProcessSpecific(t *testing.T) {
	args := []string{"template", "review", "../../deploy/charts/ops-platform", "--namespace", "sp03-review", "--set", "workloadsEnabled=true", "--set", "runtime.oidcIssuerURL=https://issuer.invalid", "--set", "runtime.profile=core"}
	for _, component := range []string{"api", "worker", "web"} {
		args = append(args, "--set", "components."+component+".image=review@sha256:"+strings.Repeat("a", 64))
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v %s", err, out)
	}
	seen := 0
	for _, doc := range strings.Split(string(out), "---") {
		if !strings.Contains(doc, "kind: Deployment") {
			continue
		}
		for _, component := range []string{"api", "worker", "web"} {
			if !strings.Contains(doc, "name: ops-"+component+"\n") {
				continue
			}
			seen++
			if component == "web" {
				if strings.Contains(doc, "DATABASE_URL") {
					t.Error("Web receives database credentials")
				}
				continue
			}
			if !strings.Contains(doc, "key: "+component+"DatabaseURL") {
				t.Errorf("%s has no distinct database credential", component)
			}
		}
	}
	if seen != 3 {
		t.Fatalf("expected 3 deployed processes, saw %d", seen)
	}
}
