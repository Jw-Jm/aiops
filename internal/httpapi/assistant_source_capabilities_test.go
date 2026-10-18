package httpapi

import (
	"testing"
	"time"

	"ops-platform/internal/investigation/tools"
)

// A capability read that consumes its owning tool's whole deadline turns a
// degradable auxiliary failure into a failed follow-up turn. The page run of
// 2026-10-18 showed the context step killed at 3007ms while the worker was
// fenced by a lease renewal, so the nested bound must stay strictly below the
// tool bound.
func TestCapabilityDetailDeadlineFitsContextTool(t *testing.T) {
	tool := tools.DeadlineFor("get_assistant_context")
	if capabilityDetailDeadline >= tool {
		t.Fatalf("capability deadline %s leaves no room inside %s", capabilityDetailDeadline, tool)
	}
	if tools.DeadlineFor("query_deepflow") != 9*time.Second {
		t.Fatalf("deepflow identity fence lost its bounded allowance: %s", tools.DeadlineFor("query_deepflow"))
	}
	if tools.DeadlineFor("get_dependencies") != 3*time.Second {
		t.Fatalf("unrelated tool deadline changed: %s", tools.DeadlineFor("get_dependencies"))
	}
}
