package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type forbiddenTxBeginner struct {
	called bool
}

func (b *forbiddenTxBeginner) Begin(context.Context) (pgx.Tx, error) {
	b.called = true
	return nil, nil
}

func TestWithTenantTxRequiresTenantBeforeOpeningTransaction(t *testing.T) {
	beginner := &forbiddenTxBeginner{}
	err := WithTenantTx(context.Background(), beginner, uuid.Nil, func(pgx.Tx) error { return nil })
	if err != ErrTenantRequired {
		t.Fatalf("expected ErrTenantRequired, got %v", err)
	}
	if beginner.called {
		t.Fatal("transaction started without an authenticated tenant")
	}
}

func TestWithTenantTxRequiresCallbackBeforeOpeningTransaction(t *testing.T) {
	beginner := &forbiddenTxBeginner{}
	err := WithTenantTx(context.Background(), beginner, uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987201"), nil)
	if err != ErrTransactionCallbackRequired {
		t.Fatalf("expected ErrTransactionCallbackRequired, got %v", err)
	}
	if beginner.called {
		t.Fatal("transaction started without a tenant callback")
	}
}
