// Package persistence contains transaction boundaries shared by tenant-scoped repositories.
package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrTenantRequired              = errors.New("tenant id is required for a business transaction")
	ErrTransactionCallbackRequired = errors.New("tenant transaction callback is required")
)

// TxBeginner is implemented by pgx pools and keeps WithTenantTx testable without a global pool.
type TxBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// WithTenantTx starts a transaction, installs a transaction-local tenant setting, runs fn,
// and commits only when fn succeeds. SET LOCAL semantics ensure a pooled connection cannot
// retain the previous tenant after commit or rollback.
func WithTenantTx(ctx context.Context, pool TxBeginner, tenantID uuid.UUID, fn func(pgx.Tx) error) (err error) {
	if tenantID == uuid.Nil {
		return ErrTenantRequired
	}
	if fn == nil {
		return ErrTransactionCallbackRequired
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tenant transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	var installed string
	if err = tx.QueryRow(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()).Scan(&installed); err != nil {
		return fmt.Errorf("set transaction tenant: %w", err)
	}
	if installed != tenantID.String() {
		return fmt.Errorf("set transaction tenant: database returned a different tenant context")
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tenant transaction: %w", err)
	}
	committed = true
	return nil
}
