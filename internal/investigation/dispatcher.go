package investigation

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"sync"
	"time"
)

type DispatchRequest struct {
	Job        Job    `json:"job"`
	JobContext string `json:"jobContext"`
	MCPContext string `json:"mcpContext"`
}
type Dispatcher struct {
	Repository Repository
	Signer     ContextSigner
	Workload   string
	Tools      []string
	Remote     func(context.Context, DispatchRequest) ([]byte, error)
}

// RunOne never holds a database transaction while invoking the investigator.
// Heartbeat loss cancels the RPC; publication still requires the current fence.
func (d Dispatcher) RunOne(ctx context.Context, tenant uuid.UUID, owner string) error {
	l, err := d.Repository.Claim(ctx, tenant, owner, 45*time.Second)
	if err != nil {
		return err
	}
	j, err := d.Repository.Get(ctx, tenant, l.JobID)
	if err != nil {
		return err
	}
	jc, err := d.Signer.IssueRegistered(ctx, d.Repository, j, l, "platform-job-api", d.Workload, d.Tools)
	if err != nil {
		_ = d.Repository.Fail(ctx, l, "CONTEXT_SIGN_UNAVAILABLE")
		return err
	}
	mc, err := d.Signer.IssueRegistered(ctx, d.Repository, j, l, "platform-mcp-gateway", d.Workload, d.Tools)
	if err != nil {
		_ = d.Repository.Fail(ctx, l, "CONTEXT_SIGN_UNAVAILABLE")
		return err
	}
	bounded, cancel := context.WithDeadline(ctx, j.ExpiresAt)
	defer cancel()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-bounded.Done():
				return
			case <-ticker.C:
				if d.Repository.Heartbeat(bounded, l, 45*time.Second) != nil {
					cancel()
					return
				}
			}
		}
	})
	result, callErr := d.Remote(bounded, DispatchRequest{j, jc, mc})
	close(done)
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	} // process shutdown leaves durable work for takeover
	if !time.Now().Before(j.ExpiresAt) {
		return d.Repository.Expire(ctx, l)
	}
	if callErr != nil {
		code := "INVESTIGATOR_FAILURE"
		if errors.Is(callErr, ErrBudget) {
			code = "BUDGET_EXHAUSTED"
		}
		_ = d.Repository.Fail(ctx, l, code)
		return callErr
	}
	final, readErr := d.Repository.Get(ctx, tenant, l.JobID)
	if readErr == nil && (final.State == "succeeded" || final.State == "partial") && len(final.Result) > 0 {
		return nil
	}
	if err = d.Repository.Complete(ctx, l, result); err != nil {
		_ = d.Repository.Fail(ctx, l, "RESULT_REJECTED")
		return err
	}
	return nil
}
