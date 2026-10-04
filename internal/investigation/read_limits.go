package investigation

import (
	"context"
	"github.com/jackc/pgx/v5"
	"slices"
)

type dataClassesKey struct{}

// WithDataClasses carries only the already verified, registered InvocationContext
// restriction. HTTP/MCP adapters install it after Authenticate; model arguments
// and public request parameters cannot set this value.
func WithDataClasses(ctx context.Context, classes []string) context.Context {
	return context.WithValue(ctx, dataClassesKey{}, slices.Clone(classes))
}
func restrictClasses(j Job, classes []string) (Job, error) {
	if len(classes) == 0 {
		return j, ErrDenied
	}
	effective := []string{}
	for _, c := range classes {
		if c != "D0" && c != "D1" {
			return j, ErrDenied
		}
		if slices.Contains(j.Budget.AllowedDataClasses, c) && !slices.Contains(effective, c) {
			effective = append(effective, c)
		}
	}
	if len(effective) == 0 {
		return j, ErrDenied
	}
	j.Budget.AllowedDataClasses = effective
	return j, nil
}

// chargeRead executes with the Job lock after current authority and Evidence
// validation. Reads use no new tool/model call, but every returned byte consumes
// the same persistent output allowance as fresh results.
func chargeRead(ctx context.Context, tx pgx.Tx, j Job, kind string, bytes int64) error {
	u := Usage{ResultBytes: bytes}
	if !j.Consumed.Add(j.Reserved).Add(u).Within(j.Budget.Usage) {
		return ErrBudget
	}
	if _, err := tx.Exec(ctx, `UPDATE investigation.jobs SET budget_consumed=$3 WHERE tenant_id=$1 AND job_id=$2`, j.TenantID, j.JobID, raw(j.Consumed.Add(u))); err != nil {
		return err
	}
	return event(ctx, tx, j, "result_read", map[string]any{"kind": kind, "consumed": u})
}
