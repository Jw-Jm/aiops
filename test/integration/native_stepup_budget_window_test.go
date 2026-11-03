package integration

import (
	"database/sql"
	"testing"
	"time"
)

// Exercise the real forward migration, including the expired lock constraint
// failure from the acceptance batch. Runtime callers still reserve the account
// lock before checking quota and comparing credentials.
func TestNativeStepUpBudgetWindowReset(t *testing.T) {
	f := newNativeMFAHardeningFixture(t)
	for _, tc := range []struct {
		name      string
		age       string
		lock      string
		count     int
		wantCount int
		remaining int
		locked    bool
	}{
		{"expired_window_and_lock", "16 minutes", "-1 minute", 5, 1, 4, false},
		{"expired_window_without_lock", "16 minutes", "", 4, 1, 4, false},
		{"open_window_below_limit", "1 minute", "", 3, 4, 1, false},
		{"open_window_reaches_limit", "1 minute", "", 4, 5, 0, true},
		{"active_lock", "1 minute", "14 minutes", 5, 6, 0, true},
		{"expired_window_live_lock", "16 minutes", "1 minute", 5, 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.adminDB.BeginTx(f.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err = tx.ExecContext(f.ctx, "SET LOCAL ROLE schema_owner"); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.ExecContext(f.ctx, "SELECT set_config('app.tenant_id',$1,true)", f.tenantA.String()); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.ExecContext(f.ctx, `
				INSERT INTO identity.step_up_failures
				(tenant_id,user_id,factor,failed_count,window_started_at,locked_until)
				VALUES ($1,$2,'password',$3,clock_timestamp()-$4::interval,
				CASE WHEN $5='' THEN NULL ELSE clock_timestamp()+$5::interval END)
				ON CONFLICT (tenant_id,user_id,factor) DO UPDATE SET
				failed_count=excluded.failed_count,window_started_at=excluded.window_started_at,
				locked_until=excluded.locked_until`, f.tenantA, f.admin.userID, tc.count, tc.age, tc.lock); err != nil {
				t.Fatal(err)
			}
			var remaining int
			var retry sql.NullTime
			if err = tx.QueryRowContext(f.ctx, `SELECT * FROM identity.step_up_budget_fail($1,$2,'password')`,
				f.tenantA, f.admin.userID).Scan(&remaining, &retry); err != nil {
				t.Fatal(err)
			}
			if remaining != tc.remaining || retry.Valid != tc.locked || (retry.Valid && !retry.Time.After(time.Now())) {
				t.Fatalf("remaining/retry = %d/%v, want %d/locked=%v", remaining, retry, tc.remaining, tc.locked)
			}
			var count int
			var lock sql.NullTime
			if err = tx.QueryRowContext(f.ctx, `SELECT failed_count,locked_until FROM identity.step_up_failures
				WHERE tenant_id=$1 AND user_id=$2 AND factor='password'`, f.tenantA, f.admin.userID).Scan(&count, &lock); err != nil {
				t.Fatal(err)
			}
			if count != tc.wantCount || lock.Valid != tc.locked {
				t.Fatalf("stored count/lock = %d/%v, want %d/locked=%v", count, lock, tc.wantCount, tc.locked)
			}
		})
	}
}
