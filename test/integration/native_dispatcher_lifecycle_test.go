package integration

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
)

// This exercises the production Dispatcher and PostgreSQL fences. Transit and
// the remote investigator remain fixtures; it is not live model/tool evidence.
func TestNativeDispatcherInFlightLifecycle(t *testing.T) {
	f := newNativeJobFixture(t)
	signer := investigation.ContextSigner{Transit: newNativeTestTransit(t), Key: "native-action-key"}
	answer := nativeAssistantAnswer(t, "迟到结果不得覆盖终态")
	dispatcher := investigation.Dispatcher{
		Repository: f.jobs, Signer: signer, Workload: nativeAssistantWorkload,
		CatalogFor: func(j investigation.Job) ([]string, error) {
			definitions, err := tools.ForJob(j)
			return tools.Names(definitions), err
		},
	}
	for _, scenario := range []string{"cancel", "revoke", "shutdown_takeover"} {
		t.Run(scenario, func(t *testing.T) {
			account := f.newAccount(t, `"apps"`)
			_, turn := f.submit(t, account, "原生运行中生命周期 "+scenario)
			defer f.quarantine(t, turn.JobID)
			entered := make(chan investigation.DispatchRequest, 1)
			remoteStopped := make(chan struct{})
			runCtx, stop := context.WithCancel(f.ctx)
			defer stop()
			d := dispatcher
			d.Remote = func(ctx context.Context, request investigation.DispatchRequest) ([]byte, error) {
				entered <- request
				<-ctx.Done()
				close(remoteStopped)
				// Deliberately return a syntactically valid late success after cancellation.
				return answer, nil
			}
			completed := make(chan error, 1)
			go func() { completed <- d.RunOne(runCtx, f.tenantID, "native-before-stop") }()
			var request investigation.DispatchRequest
			select {
			case request = <-entered:
			case err := <-completed:
				t.Fatalf("dispatcher failed before remote invocation: %v", err)
			case <-time.After(15 * time.Second):
				t.Fatal("dispatcher did not invoke remote")
			}
			if request.Job.JobID != turn.JobID || request.JobContext == "" || request.MCPContext == "" {
				t.Fatal("remote did not receive the claimed job and both registered contexts")
			}
			t.Logf("scenario=%s tenant=%s job=%s turn=%s", scenario, f.tenantID, turn.JobID, turn.TurnID)
			claims, err := signer.VerifyContext(f.ctx, request.JobContext, "platform-job-api", nativeAssistantWorkload)
			if err != nil {
				t.Fatal(err)
			}
			var cancelledAnswer []byte
			switch scenario {
			case "cancel":
				err = f.repo.Cancel(account.ctx, f.tenantID, turn.TurnID, account.subject, "native-inflight-cancel")
			case "revoke":
				_, err = f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='inflight fixture',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID)
				if err == nil {
					_, err = f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID)
				}
			case "shutdown_takeover":
				stop()
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "cancel" {
				stopped, err := f.repo.Turn(account.ctx, f.tenantID, turn.TurnID, account.subject)
				if err != nil {
					t.Fatal(err)
				}
				cancelledAnswer = append([]byte(nil), stopped.Answer...)
				if len(cancelledAnswer) == 0 {
					t.Fatal("cancel did not preserve the platform partial answer")
				}
			}
			select {
			case <-remoteStopped:
			case <-time.After(15 * time.Second):
				t.Fatal("remote context was not cancelled after authority/lease loss")
			}
			select {
			case err = <-completed:
				if err == nil {
					t.Fatal("late success was accepted")
				}
				if scenario == "shutdown_takeover" && !errors.Is(err, context.Canceled) {
					t.Fatalf("shutdown returned %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("dispatcher did not finish after remote cancellation")
			}
			// Read the persisted row independently: a revoked user must also lose
			// the ordinary Get path, while cancellation retains a platform answer.
			var state string
			var persisted []byte
			if err = f.db.QueryRowContext(f.ctx, `SELECT state,result FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, turn.JobID).Scan(&state, &persisted); err != nil {
				t.Fatal(err)
			}
			if state == "succeeded" || state == "partial" || (scenario != "cancel" && len(persisted) != 0) {
				t.Fatal("late remote success was published")
			}
			job, getErr := f.jobs.Get(f.ctx, f.tenantID, turn.JobID)
			if scenario == "revoke" {
				if !errors.Is(getErr, investigation.ErrDenied) {
					t.Fatalf("revoked Get = %v", getErr)
				}
			} else if getErr != nil {
				t.Fatal(getErr)
			}
			if scenario == "cancel" {
				readback, err := f.repo.Turn(account.ctx, f.tenantID, turn.TurnID, account.subject)
				if err != nil || readback.State != "cancelled" || !bytes.Equal(readback.Answer, cancelledAnswer) {
					t.Fatalf("cancel terminal state changed: state=%q err=%v", readback.State, err)
				}
			}
			t.Logf("scenario=%s lateResultRejected=true state=%s generation=%d", scenario, state, claims.LeaseGeneration)
			if scenario != "shutdown_takeover" {
				if _, _, err = f.jobs.Authenticate(f.ctx, claims, request.Job.ToolCatalogDigest); err == nil {
					t.Fatal("old signed context survived cancellation/revocation")
				}
				return
			}
			if job.State != "running" {
				t.Fatalf("shutdown made durable job terminal: %s", job.State)
			}
			// Expire the persisted lease to simulate restart without a 45-second wait.
			if _, err = f.db.ExecContext(f.ctx, `UPDATE investigation.worker_queue SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, turn.JobID); err != nil {
				t.Fatal(err)
			}
			recovered := false
			restarted := dispatcher
			restarted.Remote = func(ctx context.Context, next investigation.DispatchRequest) ([]byte, error) {
				if next.Job.JobID != turn.JobID {
					return nil, errors.New("restart claimed another job")
				}
				nextClaims, err := signer.VerifyContext(ctx, next.JobContext, "platform-job-api", nativeAssistantWorkload)
				if err != nil {
					return nil, err
				}
				if nextClaims.LeaseGeneration <= claims.LeaseGeneration {
					return nil, errors.New("restart did not advance the fence")
				}
				if _, _, err = f.jobs.Authenticate(ctx, claims, request.Job.ToolCatalogDigest); !errors.Is(err, investigation.ErrLease) {
					return nil, errors.New("pre-restart signed context remained valid")
				}
				if _, _, err = f.jobs.Authenticate(ctx, nextClaims, next.Job.ToolCatalogDigest); err != nil {
					return nil, err
				}
				t.Logf("takeover job=%s oldGeneration=%d newGeneration=%d oldContextRejected=true", turn.JobID, claims.LeaseGeneration, nextClaims.LeaseGeneration)
				recovered = true
				return answer, nil
			}
			if err = restarted.RunOne(f.ctx, f.tenantID, "native-after-restart"); err != nil {
				t.Fatalf("restart dispatch: %v", err)
			}
			readback, err := f.repo.Turn(account.ctx, f.tenantID, turn.TurnID, account.subject)
			if err != nil || !recovered || readback.State != "completed" || len(readback.Answer) == 0 {
				t.Fatalf("restart did not publish through new fence: state=%q err=%v", readback.State, err)
			}
		})
	}
}
