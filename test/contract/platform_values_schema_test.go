package contract

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPlatformValuesRejectTyposBeforeRendering(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("Helm is required for chart schema validation")
	}
	for _, value := range []string{"component.api.image=invalid", "sp04.graphPort=oops", "runtime.oidcIssurURL=typo", "sp06.budget.toolCall=typo"} {
		output, err := exec.Command(helm, "template", "schema-check", "../../deploy/charts/ops-platform", "--set-string", value).CombinedOutput()
		if err == nil || !strings.Contains(string(output), "schema") {
			t.Fatalf("invalid value reached rendering: %s: %v %s", value, err, output)
		}
	}
}
