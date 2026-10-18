package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
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

type blockedCapabilityPool struct{}

func (blockedCapabilityPool) Begin(ctx context.Context) (pgx.Tx, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCapabilityDeadlineIncludesRouteRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), tools.DeadlineFor("get_assistant_context"))
	defer cancel()
	h := &SP04Handlers{Pool: blockedCapabilityPool{}, Client: http.DefaultClient, EndpointAllowed: func(string) bool { return true }}
	job := investigation.Job{TenantID: uuid.New(), Scope: graph.Scope{Cluster: "cluster"}, Assistant: &investigation.AssistantTask{}}
	started := time.Now()
	_, err := h.AssistantCapabilityDetails(ctx, job)
	if !errors.Is(err, graph.ErrNotReady) || time.Since(started) > 2500*time.Millisecond || ctx.Err() != nil {
		t.Fatalf("auxiliary route read consumed the context tool deadline: elapsed=%s err=%v parent=%v", time.Since(started), err, ctx.Err())
	}
}
