package investigation

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"ops-platform/internal/audit"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"slices"
	"strconv"
	"time"
)

type Repository struct {
	Pool       persistence.TxBeginner
	Trust      configregistry.SignatureVerifier
	CurrentRCA func(context.Context, Job) (int64, error)
}

func raw(v any) []byte { b, _ := json.Marshal(v); return b }

const jobColumns = `schema_version,tenant_id,job_id,incident_id,requesting_subject,trigger_revision,trigger_kind,policy_version,effective_scope,effective_scope_digest,tool_catalog_digest,state,budget,budget_reserved,budget_consumed,expires_at,event_seq,next_call_seq,result,error_code`

func load(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, lock bool) (Job, error) {
	var j Job
	var scope, budget, reserved, consumed, result []byte
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2 AND schema_version='investigation-job/v2'`+suffix, tenant, id).Scan(&j.SchemaVersion, &j.TenantID, &j.JobID, &j.IncidentID, &j.Subject, &j.TriggerRevision, &j.TriggerKind, &j.PolicyVersion, &scope, &j.EffectiveScopeDigest, &j.ToolCatalogDigest, &j.State, &budget, &reserved, &consumed, &j.ExpiresAt, &j.EventSeq, &j.NextCallSeq, &result, &j.ErrorCode)
	if err != nil {
		return j, err
	}
	for _, v := range []struct {
		b   []byte
		out any
	}{{scope, &j.Scope}, {budget, &j.Budget}, {reserved, &j.Reserved}, {consumed, &j.Consumed}} {
		if json.Unmarshal(v.b, v.out) != nil {
			return j, ErrInvalid
		}
	}
	j.Result = result
	if classes, ok := ctx.Value(dataClassesKey{}).([]string); ok {
		return restrictClasses(j, classes)
	}
	return j, nil
}
func current(ctx context.Context, tx pgx.Tx, j Job) error {
	s, err := graph.EffectiveTx(ctx, tx, j.TenantID.String(), j.Subject, j.Scope.Cluster, true)
	if err != nil || graph.ScopeDigest(s) != j.EffectiveScopeDigest {
		return ErrDenied
	}
	vid, err := uuid.Parse(j.PolicyVersion)
	if err != nil {
		return ErrDenied
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT platform.sp06_lock_policy($1,$2,$3,$4)`, j.TenantID, vid, j.Scope.Cluster, j.Scope.Namespaces).Scan(&valid); err != nil || !valid {
		return ErrDenied
	}
	return nil
}
func checkLease(ctx context.Context, tx pgx.Tx, l Lease, j Job) error {
	if j.State != "running" {
		return ErrLease
	}
	var valid bool
	err := tx.QueryRow(ctx, `SELECT lease_token=$3 AND fencing_epoch=$4 AND lease_expires_at>clock_timestamp() AND $5::timestamptz>clock_timestamp() FROM platform.sp06_read_fence($1,$2)`, l.TenantID, l.JobID, l.Token, l.Generation, j.ExpiresAt).Scan(&valid)
	if err != nil || !valid {
		return ErrLease
	}
	return current(ctx, tx, j)
}
func event(ctx context.Context, tx pgx.Tx, j Job, kind string, payload any) error {
	var seq int64
	if err := tx.QueryRow(ctx, `UPDATE investigation.jobs SET event_seq=event_seq+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND job_id=$2 RETURNING event_seq`, j.TenantID, j.JobID).Scan(&seq); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO investigation.events(tenant_id,job_id,event_seq,event_type,payload) VALUES($1,$2,$3,$4,$5)`, j.TenantID, j.JobID, seq, kind, raw(payload)); err != nil {
		return err
	}
	_, err := audit.Append(ctx, tx, audit.Entry{TenantID: j.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "investigation." + kind, EntityKind: "investigation", EntityID: j.JobID, Subject: j.Subject, Payload: map[string]any{"jobId": j.JobID, "eventSeq": seq, "policyVersion": j.PolicyVersion, "eventDigest": graph.ScopeDigest(payload)}})
	return err
}
func (r Repository) CreateJob(ctx context.Context, q CreateInvestigationRequest) (Job, error) {
	var out Job
	if q.TenantID == uuid.Nil || q.IncidentID == uuid.Nil || q.Subject == "" || q.TriggerRevision < 1 || q.PolicyVersion == "" || q.ToolCatalogDigest == "" || !q.Budget.Valid() || q.Scope.Tenant != q.TenantID.String() || (q.TriggerKind != "manual" && q.TriggerKind != "incident_policy" && q.TriggerKind != "new_evidence") {
		return out, ErrInvalid
	}
	err := persistence.WithTenantTx(ctx, r.Pool, q.TenantID, func(tx pgx.Tx) error {
		if q.IdempotencyKey != "" {
			if len(q.IdempotencyKey) > 128 || q.RequestDigest == "" {
				return ErrInvalid
			}
			var id uuid.UUID
			var digest string
			err := tx.QueryRow(ctx, `SELECT job_id,request_digest FROM investigation.request_keys WHERE tenant_id=$1 AND subject=$2 AND idempotency_key=$3`, q.TenantID, q.Subject, q.IdempotencyKey).Scan(&id, &digest)
			if err == nil {
				if digest != q.RequestDigest {
					return ErrConflict
				}
				out, err = load(ctx, tx, q.TenantID, id, true)
				if err != nil {
					return err
				}
				return current(ctx, tx, out)
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		j := Job{SchemaVersion: "investigation-job/v2", TenantID: q.TenantID, IncidentID: q.IncidentID, Subject: q.Subject, Scope: q.Scope, EffectiveScopeDigest: graph.ScopeDigest(q.Scope), PolicyVersion: q.PolicyVersion}
		if err := current(ctx, tx, j); err != nil {
			return err
		}
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT revision FROM incident.records WHERE tenant_id=$1 AND incident_id=$2 FOR SHARE`, q.TenantID, q.IncidentID).Scan(&revision); err != nil {
			return err
		}
		if revision != q.TriggerRevision {
			return ErrInvalid
		}
		rows, err := tx.Query(ctx, `SELECT f.resource_canonical_id,f.namespace FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND l.incident_id=$2`, q.TenantID, q.IncidentID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, ns string
			if err = rows.Scan(&id, &ns); err != nil {
				rows.Close()
				return err
			}
			if !q.Scope.Allows(id, ns) {
				rows.Close()
				return ErrDenied
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		var id uuid.UUID
		err = tx.QueryRow(ctx, `INSERT INTO investigation.jobs(tenant_id,job_id,incident_id,trigger_revision,state,schema_version,requesting_subject,trigger_kind,policy_version,effective_scope,effective_scope_digest,tool_catalog_digest,budget,budget_reserved,budget_consumed,expires_at) VALUES($1,$2,$3,$4,'queued','investigation-job/v2',$5,$6,$7,$8,$9,$10,$11,$12,$12,clock_timestamp()+make_interval(secs=>$13)) ON CONFLICT(tenant_id,incident_id,policy_version,trigger_revision,trigger_kind,requesting_subject,effective_scope_digest) DO UPDATE SET updated_at=investigation.jobs.updated_at RETURNING job_id`, q.TenantID, uuid.Must(uuid.NewV7()), q.IncidentID, q.TriggerRevision, q.Subject, q.TriggerKind, q.PolicyVersion, raw(q.Scope), j.EffectiveScopeDigest, q.ToolCatalogDigest, raw(q.Budget), raw(Usage{}), q.Budget.DurationSeconds).Scan(&id)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO investigation.worker_queue(tenant_id,job_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, q.TenantID, id)
		if err != nil {
			return err
		}
		out, err = load(ctx, tx, q.TenantID, id, true)
		if err != nil {
			return err
		}
		if q.IdempotencyKey != "" {
			if _, err = tx.Exec(ctx, `INSERT INTO investigation.request_keys(tenant_id,subject,idempotency_key,request_digest,job_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, q.TenantID, q.Subject, q.IdempotencyKey, q.RequestDigest, id); err != nil {
				return err
			}
			var mapped uuid.UUID
			var digest string
			if err = tx.QueryRow(ctx, `SELECT job_id,request_digest FROM investigation.request_keys WHERE tenant_id=$1 AND subject=$2 AND idempotency_key=$3`, q.TenantID, q.Subject, q.IdempotencyKey).Scan(&mapped, &digest); err != nil {
				return err
			}
			if mapped != id || digest != q.RequestDigest {
				return ErrConflict
			}
		}
		if tag.RowsAffected() == 1 {
			return event(ctx, tx, out, "queued", map[string]any{"state": "queued"})
		}
		return nil
	})
	return out, err
}
func (r Repository) Get(ctx context.Context, tenant, id uuid.UUID) (Job, error) {
	var j Job
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		var e error
		j, e = load(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if err := current(ctx, tx, j); err != nil {
			return err
		}
		if len(j.Result) > 0 {
			if _, err := readSteps(ctx, tx, j); err != nil {
				return err
			}
			return authorizeResult(ctx, tx, j, j.Result)
		}
		return nil
	})
	return j, err
}

func (r Repository) Authenticate(ctx context.Context, c InvocationClaims, catalog string) (Job, Lease, error) {
	var j Job
	var l Lease
	err := persistence.WithTenantTx(ctx, r.Pool, c.TenantID, func(tx pgx.Tx) error {
		var err error
		j, err = load(ctx, tx, c.TenantID, c.JobID, true)
		if err != nil {
			return err
		}
		l = Lease{TenantID: c.TenantID, JobID: c.JobID}
		if err = tx.QueryRow(ctx, `SELECT lease_token,fencing_epoch FROM platform.sp06_read_fence($1,$2)`, c.TenantID, c.JobID).Scan(&l.Token, &l.Generation); err != nil {
			return ErrLease
		}
		if err = c.Bind(j, l, catalog, ""); err != nil {
			return err
		}
		var issued bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.contexts WHERE tenant_id=$1 AND job_id=$2 AND context_id=$3 AND session_nonce=$4 AND claims_digest=$5 AND lease_generation=$6 AND expires_at>clock_timestamp())`, c.TenantID, c.JobID, c.ContextID, c.SessionNonce, graph.ScopeDigest(c), l.Generation).Scan(&issued); err != nil || !issued {
			return ErrLease
		}
		if err = checkLease(ctx, tx, l, j); err != nil {
			return err
		}
		j, err = restrictClasses(j, c.AllowedDataClasses)
		return err
	})
	return j, l, err
}

func (r Repository) Steps(ctx context.Context, l Lease) ([]Step, error) {
	out := []Step{}
	err := r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		var err error
		out, err = readSteps(ctx, tx, j)
		if err != nil {
			return err
		}
		return chargeRead(ctx, tx, j, "steps", int64(len(raw(out))))
	})
	if errors.Is(err, ErrBudget) {
		_ = r.ExhaustBudget(ctx, l)
	}
	if err != nil {
		out = nil
	}
	return out, err
}

func (r Repository) Claim(ctx context.Context, tenant uuid.UUID, owner string, ttl time.Duration) (Lease, error) {
	var l Lease
	if owner == "" || ttl <= 0 || ttl > time.Minute {
		return l, ErrInvalid
	}
	var claimError error
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT j.job_id FROM investigation.jobs j JOIN investigation.worker_queue q USING(tenant_id,job_id) WHERE j.tenant_id=$1 AND j.schema_version='investigation-job/v2' AND j.state IN('queued','running') AND j.expires_at>clock_timestamp() AND q.next_attempt_at<=clock_timestamp() AND(q.lease_expires_at IS NULL OR q.lease_expires_at<=clock_timestamp()) ORDER BY j.created_at FOR UPDATE OF j,q SKIP LOCKED LIMIT 1`, tenant).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoJob
		}
		if err != nil {
			return err
		}
		j, err := load(ctx, tx, tenant, id, true)
		if err != nil {
			return err
		}
		if err = current(ctx, tx, j); err != nil {
			if err = settleUnknown(ctx, tx, j); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `SELECT platform.sp06_rotate_fence($1,$2,$3)`, tenant, id, uuid.New()); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE investigation.jobs SET state='failed',error_code='AUTHORIZATION_REVOKED' WHERE tenant_id=$1 AND job_id=$2`, tenant, id); err != nil {
				return err
			}
			claimError = ErrDenied
			return event(ctx, tx, j, "failed", map[string]any{"state": "failed", "errorCode": "AUTHORIZATION_REVOKED", "partial": true})
		}
		l = Lease{TenantID: tenant, JobID: id, Token: uuid.New()}
		err = tx.QueryRow(ctx, `UPDATE investigation.worker_queue SET lease_owner=$3,lease_token=$4,lease_expires_at=LEAST(clock_timestamp()+make_interval(secs=>$5),$6),heartbeat_at=clock_timestamp(),fencing_epoch=fencing_epoch+1,attempts=attempts+1 WHERE tenant_id=$1 AND job_id=$2 RETURNING fencing_epoch`, tenant, id, owner, l.Token, ttl.Seconds(), j.ExpiresAt).Scan(&l.Generation)
		if err != nil {
			return err
		}
		// An interrupted remote call may have consumed its entire reservation. Never
		// reset budget on takeover; settle unknown attempts conservatively.
		if err = settleUnknown(ctx, tx, j); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE investigation.jobs SET state='running' WHERE tenant_id=$1 AND job_id=$2`, tenant, id); err != nil {
			return err
		}
		return event(ctx, tx, j, "running", map[string]any{"state": "running", "leaseGeneration": l.Generation})
	})
	if err == nil && claimError != nil {
		return l, claimError
	}
	return l, err
}

func (r Repository) Heartbeat(ctx context.Context, l Lease, ttl time.Duration) error {
	if ttl <= 0 || ttl > time.Minute {
		return ErrInvalid
	}
	return r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		_, err := tx.Exec(ctx, `UPDATE investigation.worker_queue SET heartbeat_at=clock_timestamp(),lease_expires_at=LEAST(clock_timestamp()+make_interval(secs=>$3),$4) WHERE tenant_id=$1 AND job_id=$2`, l.TenantID, l.JobID, ttl.Seconds(), j.ExpiresAt)
		return err
	})
}
func (r Repository) withLease(ctx context.Context, l Lease, fn func(pgx.Tx, Job) error) error {
	exhausted := false
	err := persistence.WithTenantTx(ctx, r.Pool, l.TenantID, func(tx pgx.Tx) error {
		j, err := load(ctx, tx, l.TenantID, l.JobID, true)
		if err != nil {
			return err
		}
		if err = checkLease(ctx, tx, l, j); err != nil {
			return err
		}
		err = fn(tx, j)
		if errors.Is(err, ErrBudget) {
			exhausted = true
			return exhaustBudgetTx(ctx, tx, j)
		}
		return err
	})
	if err == nil && exhausted {
		return ErrBudget
	}
	return err
}
func replayError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) && p.Code == "23505" {
		return ErrReplay
	}
	return err
}
func (r Repository) BeginCall(ctx context.Context, l Lease, c Call) (Step, error) {
	var step Step
	if c.StepID == uuid.Nil || c.ContextID == uuid.Nil || c.Name == "" || c.ArgsDigest == "" || (c.Seq == nil) == (c.JTI == "") || len(c.JTI) > 128 || !c.Reserve.Within(DefaultBudget().Usage) || (c.Model && (c.Reserve.ModelRequests != 1 || c.Reserve.ToolCalls != 0)) || (!c.Model && (c.Reserve.ToolCalls != 1 || c.Reserve.ModelRequests != 0)) {
		return step, ErrInvalid
	}
	var admissionError error
	err := r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		if c.Model {
			var duplicate bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='model' AND args_digest=$3 AND (state='succeeded' OR (state='running' AND lease_generation=$4)))`, j.TenantID, j.JobID, c.ArgsDigest, l.Generation).Scan(&duplicate); err != nil {
				return err
			}
			if duplicate {
				return ErrReplay
			}
		}
		if c.Claims != nil {
			if !c.Claims.Valid(time.Now()) {
				return ErrLease
			}
			if err := c.Claims.Bind(j, l, j.ToolCatalogDigest, c.Name); err != nil {
				return err
			}
			restricted, err := restrictClasses(j, c.Claims.AllowedDataClasses)
			if err != nil {
				return err
			}
			j = restricted
			var allocated bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.call_allocations WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3 AND ($4='' OR jti=$4) AND tool_name=$5 AND args_digest=$6 AND lease_generation=$7 AND NOT model)`, l.TenantID, l.JobID, c.StepID, c.JTI, c.Name, c.ArgsDigest, l.Generation).Scan(&allocated); err != nil || !allocated {
				return ErrReplay
			}
		}
		kind, value := "jti", c.JTI
		if c.Seq != nil {
			kind = "seq"
			value = strconv.FormatInt(*c.Seq, 10)
			if *c.Seq != j.NextCallSeq+1 {
				return ErrReplay
			}
		}
		var repeated bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.replay_ledger WHERE tenant_id=$1 AND context_id=$2 AND nonce_kind=$3 AND nonce_value=$4)`, l.TenantID, c.ContextID, kind, value).Scan(&repeated); err != nil {
			return err
		}
		if repeated {
			return ErrReplay
		}
		var state, args string
		err := tx.QueryRow(ctx, `SELECT state,args_digest FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3`, l.TenantID, l.JobID, c.StepID).Scan(&state, &args)
		if err == nil {
			if args != c.ArgsDigest {
				return ErrInvalid
			}
			if state == "succeeded" {
				return ErrCommitted
			}
			if state == "running" {
				var generation int64
				if err = tx.QueryRow(ctx, `SELECT lease_generation FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3`, l.TenantID, l.JobID, c.StepID).Scan(&generation); err != nil {
					return err
				}
				if generation == l.Generation {
					return ErrReplay
				}
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !j.Consumed.Add(j.Reserved).Add(c.Reserve).Within(j.Budget.Usage) {
			admissionError = ErrBudget
			return exhaustBudgetTx(ctx, tx, j)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO investigation.steps(tenant_id,job_id,step_id,tool_name,args_digest,state,lease_token,lease_generation) VALUES($1,$2,$3,$4,$5,'running',$6,$7) ON CONFLICT(tenant_id,job_id,step_id) DO UPDATE SET state='running',lease_token=EXCLUDED.lease_token,lease_generation=EXCLUDED.lease_generation,retry_count=investigation.steps.retry_count+1,started_at=clock_timestamp(),completed_at=NULL,error_code=''`, l.TenantID, l.JobID, c.StepID, c.Name, c.ArgsDigest, l.Token, l.Generation); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO investigation.replay_ledger(tenant_id,context_id,nonce_kind,nonce_value,job_id,step_id,args_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, l.TenantID, c.ContextID, kind, value, l.JobID, c.StepID, c.ArgsDigest); err != nil {
			return replayError(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO investigation.admissions(tenant_id,job_id,admission_id,step_id,context_id,call_seq,call_jti,lease_generation,reservation,state,allowed_data_classes) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,'reserved',$10)`, l.TenantID, l.JobID, uuid.Must(uuid.NewV7()), c.StepID, c.ContextID, c.Seq, c.JTI, l.Generation, raw(c.Reserve), j.Budget.AllowedDataClasses); err != nil {
			return replayError(err)
		}
		next := j.NextCallSeq
		if c.Seq != nil {
			next = *c.Seq
		}
		if _, err = tx.Exec(ctx, `UPDATE investigation.jobs SET budget_reserved=$3,next_call_seq=$4 WHERE tenant_id=$1 AND job_id=$2`, l.TenantID, l.JobID, raw(j.Reserved.Add(c.Reserve)), next); err != nil {
			return err
		}
		step = Step{StepID: c.StepID, Name: c.Name, ArgsDigest: c.ArgsDigest, State: "running"}
		return event(ctx, tx, j, "step_started", map[string]any{"stepId": c.StepID, "toolName": c.Name, "argsDigest": c.ArgsDigest, "leaseGeneration": l.Generation})
	})
	if err == nil && admissionError != nil {
		return step, admissionError
	}
	return step, err
}
func (r Repository) CompleteStep(ctx context.Context, l Lease, id uuid.UUID, result []byte, used Usage) error {
	return r.settle(ctx, l, id, result, &used, "")
}

// CompleteToolStep returns the freshly committed output in the settlement
// transaction. Its first disclosure is covered by the existing reservation;
// subsequent recovery reads use RecoverStep and charge additional output bytes.
func (r Repository) CompleteToolStep(ctx context.Context, l Lease, id uuid.UUID, result []byte, used Usage) (json.RawMessage, error) {
	var committed []byte
	err := r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		if !json.Valid(result) {
			return ErrInvalid
		}
		if err := r.settleTx(ctx, tx, j, l, id, result, &used, ""); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3 AND state='succeeded'`, l.TenantID, l.JobID, id).Scan(&committed)
	})
	if errors.Is(err, ErrBudget) {
		_ = r.ExhaustBudget(ctx, l)
	}
	if err != nil {
		committed = nil
	}
	return committed, err
}
func (r Repository) FailStep(ctx context.Context, l Lease, id uuid.UUID, code string) error {
	if code == "" {
		return ErrInvalid
	}
	return r.settle(ctx, l, id, nil, nil, code)
}
func (r Repository) settle(ctx context.Context, l Lease, id uuid.UUID, result []byte, used *Usage, code string) error {
	if code == "" && !json.Valid(result) {
		return ErrInvalid
	}
	err := r.withLease(ctx, l, func(tx pgx.Tx, j Job) error { return r.settleTx(ctx, tx, j, l, id, result, used, code) })
	if errors.Is(err, ErrBudget) {
		_ = r.ExhaustBudget(ctx, l)
	}
	return err
}
func (r Repository) settleTx(ctx context.Context, tx pgx.Tx, j Job, l Lease, id uuid.UUID, result []byte, used *Usage, code string) error {
	var reserve Usage
	var b []byte
	var admission uuid.UUID
	var classes []string
	err := tx.QueryRow(ctx, `SELECT admission_id,reservation,allowed_data_classes FROM investigation.admissions WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3 AND lease_generation=$4 AND state='reserved' FOR UPDATE`, l.TenantID, l.JobID, id, l.Generation).Scan(&admission, &b, &classes)
	if err != nil {
		return ErrLease
	}
	j, err = restrictClasses(j, classes)
	if err != nil {
		return err
	}
	if json.Unmarshal(b, &reserve) != nil {
		return ErrInvalid
	}
	actual := reserve
	if used != nil {
		actual = *used
		actual.ToolCalls = reserve.ToolCalls
		actual.ModelRequests = reserve.ModelRequests
		actual.ResultBytes = max(actual.ResultBytes, int64(len(result)))
		if !actual.Within(reserve) || int64(len(result)) > reserve.ResultBytes {
			return ErrBudget
		}
	}
	if code == "" {
		var envelope map[string]any
		if json.Unmarshal(result, &envelope) == nil && envelope["schemaVersion"] == "tool-response/v2" {
			// The response's budget is derived under the same Job lock as its
			// settlement. Cached successful results preserve this exact ledger view.
			for range 6 {
				envelope["budget"] = map[string]any{"consumed": actual, "remaining": j.Budget.Usage.Sub(j.Consumed.Add(actual).Add(j.Reserved.Sub(reserve)))}
				result = raw(envelope)
				if actual.ResultBytes == int64(len(result)) {
					break
				}
				actual.ResultBytes = int64(len(result))
			}
			if !actual.Within(reserve) {
				return ErrBudget
			}
		}
	}
	if code == "" { // Evidence retention is committed atomically with the successful Step.
		var refs struct {
			EvidenceRefs []string `json:"evidenceRefs"`
		}
		if json.Unmarshal(result, &refs) != nil {
			return ErrInvalid
		}
		for _, ref := range refs.EvidenceRefs {
			eid, err := uuid.Parse(ref)
			if err != nil {
				return ErrInvalid
			}
			ev, err := evidence.GetTx(ctx, tx, j.TenantID, eid, j.Scope)
			if err != nil || !slices.Contains(j.Budget.AllowedDataClasses, ev.DataClass) {
				return ErrDenied
			}
			if err = evidence.Protect(ctx, tx, j.TenantID, eid, id, "investigation", time.Now().Add(365*24*time.Hour), true); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE investigation.admissions SET state='settled',consumed=$4 WHERE tenant_id=$1 AND job_id=$2 AND admission_id=$3`, l.TenantID, l.JobID, admission, raw(actual)); err != nil {
		return err
	}
	state := "succeeded"
	if code != "" {
		state = "failed"
	}
	var payload any
	if code == "" {
		payload = json.RawMessage(result)
	}
	tag, err := tx.Exec(ctx, `UPDATE investigation.steps SET state=$5,result=$6,result_digest=$7,error_code=$8,completed_at=clock_timestamp() WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3 AND lease_generation=$4 AND lease_token=$9 AND state='running'`, l.TenantID, l.JobID, id, l.Generation, state, payload, graph.ScopeDigest(json.RawMessage(result)), code, l.Token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLease
	}
	if _, err = tx.Exec(ctx, `UPDATE investigation.jobs SET budget_reserved=$3,budget_consumed=$4 WHERE tenant_id=$1 AND job_id=$2`, l.TenantID, l.JobID, raw(j.Reserved.Sub(reserve)), raw(j.Consumed.Add(actual))); err != nil {
		return err
	}
	return event(ctx, tx, j, "step_"+state, map[string]any{"stepId": id, "resultDigest": graph.ScopeDigest(json.RawMessage(result)), "errorCode": code, "consumed": actual})
}
func (r Repository) finish(ctx context.Context, l Lease, state, code string) error {
	return r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		if err := settleUnknown(ctx, tx, j); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE investigation.jobs SET state=$3,error_code=$4 WHERE tenant_id=$1 AND job_id=$2`, l.TenantID, l.JobID, state, code); err != nil {
			return err
		}
		return event(ctx, tx, j, state, map[string]any{"state": state, "errorCode": code, "partial": true})
	})
}
func (r Repository) Fail(ctx context.Context, l Lease, code string) error {
	if code == "" {
		return ErrInvalid
	}
	return r.finish(ctx, l, "failed", code)
}
func (r Repository) Cancel(ctx context.Context, l Lease) error {
	return r.finish(ctx, l, "cancelled", "CANCELLED")
}

// Expire fences with the observed generation and token, even after the lease
// deadline; the row lock makes cancel/takeover/timeout races deterministic.
func (r Repository) Expire(ctx context.Context, l Lease) error {
	return persistence.WithTenantTx(ctx, r.Pool, l.TenantID, func(tx pgx.Tx) error {
		j, err := load(ctx, tx, l.TenantID, l.JobID, true)
		if err != nil {
			return err
		}
		var valid bool
		err = tx.QueryRow(ctx, `SELECT lease_token=$3 AND fencing_epoch=$4 AND $5::timestamptz<=clock_timestamp() FROM platform.sp06_read_fence($1,$2)`, l.TenantID, l.JobID, l.Token, l.Generation, j.ExpiresAt).Scan(&valid)
		if err != nil || !valid || j.State != "running" {
			return ErrLease
		}
		if err = settleUnknown(ctx, tx, j); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE investigation.jobs SET state='expired',error_code='TIME_BUDGET_EXHAUSTED' WHERE tenant_id=$1 AND job_id=$2`, l.TenantID, l.JobID); err != nil {
			return err
		}
		return event(ctx, tx, j, "expired", map[string]any{"state": "expired", "partial": true})
	})
}
