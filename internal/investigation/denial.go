package investigation

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// RecordDenial does not consume a nonce or admit a call. An authenticated current
// lease still fences the durable audit; no caller text or credentials are saved.
func (r Repository) RecordDenial(ctx context.Context, l Lease, tool, code string) error {
	if len(tool) > 128 || len(code) > 128 {
		return ErrInvalid
	}
	return r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		return event(ctx, tx, j, "call.denied", map[string]any{"tool": tool, "errorCode": code})
	})
}
