package integration

// MR-020 / MR-014 concurrency and recovery regressions (second review round).
//
// The comparison-boundary proof wraps the real FileSecretSealer: Open() is the
// exact point where a TOTP challenge secret is read, i.e. immediately before the
// credential comparison. Each handler instance owns its own wrapper, so a test
// can hold the first request at that boundary while it owns the account row lock,
// start a second request that must contend for the same lock, release, and then
// assert directly that the loser never reached Open. Counting attempts or
// failures alone cannot prove that, because a losing implementation that compared
// and then rolled back would show the same counters.
//
// Everything is opt-in (native identity test guard) and needs a real PostgreSQL
// instance; a default `go test` only compiles it.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/identity"
)

type observingSealer struct {
	inner identity.SecretSealer

	mu      sync.Mutex
	opened  int
	gate    chan struct{}
	entered chan uuid.UUID
}

func newObservingSealer(inner identity.SecretSealer) *observingSealer {
	return &observingSealer{inner: inner, entered: make(chan uuid.UUID, 16)}
}

func (s *observingSealer) Seal(ctx context.Context, tenantID, userID, enrollmentID uuid.UUID, plain []byte) (identity.SecretEnvelope, error) {
	return s.inner.Seal(ctx, tenantID, userID, enrollmentID, plain)
}

// Open is the comparison boundary: a challenge verification cannot compare a TOTP
// code without opening (and therefore counting) its sealed secret here.
func (s *observingSealer) Open(ctx context.Context, tenantID, userID, enrollmentID uuid.UUID, envelope identity.SecretEnvelope) ([]byte, error) {
	s.mu.Lock()
	s.opened++
	gate := s.gate
	s.mu.Unlock()
	select {
	case s.entered <- enrollmentID:
	default:
	}
	if gate != nil {
		<-gate
	}
	return s.inner.Open(ctx, tenantID, userID, enrollmentID, envelope)
}

func (s *observingSealer) openCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened
}

// mark returns a baseline. Assertions use increments since a mark because the
// fixture itself opens sealed secrets during administrator and target enrolment.
func (s *observingSealer) mark() int { return s.openCount() }

func (s *observingSealer) openedSince(mark int) int { return s.openCount() - mark }

func (s *observingSealer) hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gate == nil {
		s.gate = make(chan struct{})
	}
}

func (s *observingSealer) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gate != nil {
		close(s.gate)
		s.gate = nil
	}
}

// waitEntered blocks until one Open reached the comparison boundary. Enrollment
// flows call Open too, so callers drain it before arming the gate.
func (s *observingSealer) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-s.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("the request never reached the TOTP comparison boundary")
	}
}

func (s *observingSealer) drain() {
	for {
		select {
		case <-s.entered:
		default:
			return
		}
	}
}

type nativeMFAVerifyOutcome struct {
	status  int
	payload map[string]any
	body    string
	done    time.Time
}

func nativeMFAHardeningAttempts(t *testing.T, f *nativeMFAHardeningFixture, challengeID string) int {
	t.Helper()
	return mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT attempts FROM identity.step_up_challenges
		WHERE tenant_id=$1 AND challenge_id=$2
	`, f.tenantA, challengeID)
}

func nativeMFAHardeningRevision(t *testing.T, f *nativeMFAHardeningFixture, challengeID string) int {
	t.Helper()
	return mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT revision FROM identity.step_up_challenges
		WHERE tenant_id=$1 AND challenge_id=$2
	`, f.tenantA, challengeID)
}

func nativeMFAHardeningBudget(t *testing.T, f *nativeMFAHardeningFixture, userID uuid.UUID, factor string) int {
	t.Helper()
	return mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT COALESCE(max(failed_count), 0) FROM identity.step_up_failures
		WHERE tenant_id=$1 AND user_id=$2 AND factor=$3
	`, f.tenantA, userID, factor)
}

// nativeMFAHardeningOwnerExec runs one controlled setup/evidence write as
// schema_owner with an explicit tenant context, which FORCE RLS requires.
func nativeMFAHardeningOwnerExec(t *testing.T, f *nativeMFAHardeningFixture, tenantID uuid.UUID, query string, args ...any) {
	t.Helper()
	tx, err := f.adminDB.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(f.ctx, "SET LOCAL ROLE schema_owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(f.ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func nativeMFAHardeningEnrolled(t *testing.T, f *nativeMFAHardeningFixture, prefix, password string) (string, uuid.UUID, mfaSession, string) {
	t.Helper()
	login := prefix + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	userID := nativeMFAHardeningAccount(t, f.ctx, f.bootstrapPool, f.tenantA, login, password)
	session, _ := mfaLoginWithCookie(t, f.client, login, password)
	secret, _ := mfaEnrolFactor(t, f.client, &session, password)
	if secret == "" {
		t.Fatal("enrolment returned no secret")
	}
	f.sealerA.drain()
	f.sealerB.drain()
	return login, userID, session, secret
}

// startVerify runs one verification in the background; the caller releases the
// gate and then collects the result.
func startVerify(t *testing.T, client mfaClient, session *mfaSession, challengeID string, revision int, method, credential string) chan nativeMFAVerifyOutcome {
	t.Helper()
	results := make(chan nativeMFAVerifyOutcome, 1)
	go func() {
		status, payload, body := nativeMFAHardeningVerify(t, client, session, challengeID, revision, method, credential)
		results <- nativeMFAVerifyOutcome{status: status, payload: payload, body: body, done: time.Now()}
	}()
	return results
}

func awaitVerify(t *testing.T, results chan nativeMFAVerifyOutcome) nativeMFAVerifyOutcome {
	t.Helper()
	select {
	case outcome := <-results:
		return outcome
	case <-time.After(30 * time.Second):
		t.Fatal("verification did not finish")
		return nativeMFAVerifyOutcome{}
	}
}

// TestNativeStepUpSameChallengeComparesOnce holds the winner at the TOTP secret
// boundary while it owns the account lock, proves the second request is blocked,
// and then proves directly that the loser never reached that boundary.
func TestNativeStepUpSameChallengeComparesOnce(t *testing.T) {
	f := newNativeMFAHardeningFixture(t)
	password := "mfa-hardening-race-password"
	_, userID, session, secret := nativeMFAHardeningEnrolled(t, f, "mfa.race.same.", password)
	wrong := nativeMFAHardeningWrongCode(t, secret)

	challengeID, revision := nativeMFAHardeningChallenge(t, f.client, &session)
	markA := f.sealerA.mark()
	markB := f.sealerB.mark()
	f.sealerA.hold()
	defer f.sealerA.release()
	winner := startVerify(t, f.client, &session, challengeID, revision, "totp", wrong)
	f.sealerA.waitEntered(t)
	loser := startVerify(t, f.secondClient, &session, challengeID, revision, "totp", wrong)
	// The loser must be blocked behind the account lock, not finished: a finished
	// loser would mean the account lock no longer serializes verification.
	select {
	case outcome := <-loser:
		t.Fatalf("the second request finished while the first held the account lock: %d %s", outcome.status, outcome.body)
	case <-time.After(300 * time.Millisecond):
	}
	f.sealerA.release()

	won := awaitVerify(t, winner)
	lost := awaitVerify(t, loser)
	if won.status != http.StatusOK || mfaData(t, won.payload)["verified"] == true {
		t.Fatalf("winner = %d %s, want a truthful failed verification", won.status, won.body)
	}
	if lost.status != http.StatusConflict {
		t.Fatalf("loser = %d %s, want 409", lost.status, lost.body)
	}
	if opened := f.sealerB.openedSince(markB); opened != 0 {
		t.Fatalf("the losing request opened %d sealed secrets: it compared a credential", opened)
	}
	if opened := f.sealerA.openedSince(markA); opened != 1 {
		t.Fatalf("winner opened %d sealed secrets, want 1", opened)
	}
	if attempts := nativeMFAHardeningAttempts(t, f, challengeID); attempts != 1 {
		t.Fatalf("challenge attempts = %d, want 1", attempts)
	}
	if counted := nativeMFAHardeningBudget(t, f, userID, "totp"); counted != 1 {
		t.Fatalf("persisted failures = %d, want 1", counted)
	}
	if current := nativeMFAHardeningRevision(t, f, challengeID); current != revision+1 {
		t.Fatalf("challenge revision = %d, want exactly one advance to %d", current, revision+1)
	}

	// Replaying the same (now stale) revision stays refused and never compares.
	before := f.sealerB.mark()
	if status, _, body := nativeMFAHardeningVerify(t, f.secondClient, &session, challengeID, revision, "totp", wrong); status != http.StatusConflict {
		t.Fatalf("stale revision after the race = %d: %s", status, body)
	}
	if opened := f.sealerB.openedSince(before); opened != 0 {
		t.Fatalf("a stale revision reached the comparison boundary (%d opens)", opened)
	}

	// A correct code raced the same way is compared exactly once, consumes the time
	// step once and leaves the failure counter alone.
	successCode := mfaCurrentCode(t, secret)
	successChallenge, successRevision := nativeMFAHardeningChallenge(t, f.client, &session)
	f.sealerA.drain()
	successMarkA := f.sealerA.mark()
	successMarkB := f.sealerB.mark()
	f.sealerA.hold()
	defer f.sealerA.release()
	successWinner := startVerify(t, f.client, &session, successChallenge, successRevision, "totp", successCode)
	f.sealerA.waitEntered(t)
	successLoser := startVerify(t, f.secondClient, &session, successChallenge, successRevision, "totp", successCode)
	select {
	case outcome := <-successLoser:
		t.Fatalf("the second correct request finished while the first held the lock: %d %s", outcome.status, outcome.body)
	case <-time.After(300 * time.Millisecond):
	}
	f.sealerA.release()
	wonSuccess := awaitVerify(t, successWinner)
	lostSuccess := awaitVerify(t, successLoser)
	if wonSuccess.status != http.StatusOK {
		t.Fatalf("successful winner = %d %s", wonSuccess.status, wonSuccess.body)
	}
	if lostSuccess.status != http.StatusConflict {
		t.Fatalf("loser of the correct race = %d %s, want 409", lostSuccess.status, lostSuccess.body)
	}
	if opened := f.sealerB.openedSince(successMarkB); opened != 0 {
		t.Fatalf("the losing correct request reached the comparison boundary (%d opens)", opened)
	}
	if opened := f.sealerA.openedSince(successMarkA); opened != 1 {
		t.Fatalf("the winning correct request opened %d sealed secrets, want 1", opened)
	}
	if attempts := nativeMFAHardeningAttempts(t, f, successChallenge); attempts != 0 {
		t.Fatalf("a verified method left %d attempts, want 0", attempts)
	}
	if counted := nativeMFAHardeningBudget(t, f, userID, "totp"); counted != 1 {
		t.Fatalf("a correct verification changed the failure count to %d, want 1", counted)
	}

	// Replaying the consumed time step is still rejected and counts as one failure.
	replayID, replayRevision := nativeMFAHardeningChallenge(t, f.client, &session)
	if status, _, body := nativeMFAHardeningVerify(t, f.client, &session, replayID, replayRevision, "password", password); status != http.StatusOK {
		t.Fatalf("replay password step = %d: %s", status, body)
	}
	replayRevision++
	status, payload, body := nativeMFAHardeningVerify(t, f.client, &session, replayID, replayRevision, "totp", successCode)
	if status != http.StatusOK || mfaData(t, payload)["verified"] != false {
		t.Fatalf("replayed TOTP = %d: %s", status, body)
	}
	if counted := nativeMFAHardeningBudget(t, f, userID, "totp"); counted != 2 {
		t.Fatalf("replay counted %d failures, want 2", counted)
	}
}

// TestNativeStepUpRefusalsNeverCompare uses the same boundary observer for stale,
// expired, replaced and quota-refused requests.
func TestNativeStepUpRefusalsNeverCompare(t *testing.T) {
	f := newNativeMFAHardeningFixture(t)
	password := "mfa-hardening-stale-password"
	_, userID, session, secret := nativeMFAHardeningEnrolled(t, f, "mfa.stale.", password)

	// Stale revision.
	challengeID, revision := nativeMFAHardeningChallenge(t, f.client, &session)
	mark := f.sealerA.mark()
	if status, _, body := nativeMFAHardeningVerify(t, f.client, &session, challengeID, revision+7, "totp", mfaCurrentCode(t, secret)); status != http.StatusConflict {
		t.Fatalf("stale revision = %d: %s", status, body)
	}
	if opened := f.sealerA.openedSince(mark); opened != 0 {
		t.Fatalf("stale revision reached the comparison boundary (%d opens)", opened)
	}
	if attempts := nativeMFAHardeningAttempts(t, f, challengeID); attempts != 0 {
		t.Fatalf("stale revision consumed %d attempts", attempts)
	}
	if counted := nativeMFAHardeningBudget(t, f, userID, "totp"); counted != 0 {
		t.Fatalf("stale revision counted %d failures", counted)
	}

	// Expired challenge: idle expiry moved into the past by the controlled duty.
	expiredID, expiredRevision := nativeMFAHardeningChallenge(t, f.client, &session)
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		UPDATE identity.step_up_challenges
		SET idle_expires_at = clock_timestamp() - interval '1 minute'
		WHERE tenant_id=$1 AND challenge_id=$2
	`, f.tenantA, expiredID)
	mark = f.sealerA.mark()
	if status, _, body := nativeMFAHardeningVerify(t, f.client, &session, expiredID, expiredRevision, "totp", mfaCurrentCode(t, secret)); status != http.StatusConflict {
		t.Fatalf("expired challenge = %d: %s", status, body)
	}
	if opened := f.sealerA.openedSince(mark); opened != 0 {
		t.Fatalf("expired challenge reached the comparison boundary (%d opens)", opened)
	}

	// Replaced challenge: creating the next pending challenge revokes the old one,
	// so verifying the old revision must be refused without opening the secret.
	replacedID, replacedRevision := nativeMFAHardeningChallenge(t, f.client, &session)
	if status, _, _ := nativeMFAHardeningChallengeBudget(t, f.client, &session); status != http.StatusCreated {
		t.Fatalf("replacement challenge = %d, want 201", status)
	}
	mark = f.sealerA.mark()
	if status, _, body := nativeMFAHardeningVerify(t, f.client, &session, replacedID, replacedRevision, "totp", mfaCurrentCode(t, secret)); status != http.StatusConflict {
		t.Fatalf("replaced challenge = %d: %s", status, body)
	}
	if opened := f.sealerA.openedSince(mark); opened != 0 {
		t.Fatalf("replaced challenge reached the comparison boundary (%d opens)", opened)
	}

	// Locked factor: a pending challenge exists, then the budget is locked, and the
	// verification is refused before opening the secret.
	lockedID, lockedRevision := nativeMFAHardeningChallengeFor(t, f.client, &session, "mfa_reset")
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		INSERT INTO identity.step_up_failures
			(tenant_id, user_id, factor, failed_count, window_started_at, locked_until)
		VALUES ($1,$2,'totp',5, clock_timestamp() - interval '1 minute', clock_timestamp() + interval '5 minutes')
		ON CONFLICT (tenant_id,user_id,factor) DO UPDATE
		SET failed_count=5, window_started_at=clock_timestamp() - interval '1 minute',
		    locked_until=clock_timestamp() + interval '5 minutes'
	`, f.tenantA, userID)
	mark = f.sealerA.mark()
	if status, _, body := nativeMFAHardeningVerify(t, f.client, &session, lockedID, lockedRevision, "totp", mfaCurrentCode(t, secret)); status != http.StatusTooManyRequests {
		t.Fatalf("locked factor verify = %d: %s", status, body)
	}
	if opened := f.sealerA.openedSince(mark); opened != 0 {
		t.Fatalf("a locked factor still reached the comparison boundary (%d opens)", opened)
	}
	if attempts := nativeMFAHardeningAttempts(t, f, lockedID); attempts != 0 {
		t.Fatalf("a locked factor consumed %d attempts", attempts)
	}
}

// TestNativeStepUpInFlightReplacementKeepsTheFailure holds a wrong verification at
// the comparison boundary while a replacement challenge is queued behind the same
// account lock. The executed failure must survive the replacement.
func TestNativeStepUpInFlightReplacementKeepsTheFailure(t *testing.T) {
	f := newNativeMFAHardeningFixture(t)
	password := "mfa-hardening-replace-password"
	_, userID, session, secret := nativeMFAHardeningEnrolled(t, f, "mfa.replace.", password)
	wrong := nativeMFAHardeningWrongCode(t, secret)

	challengeID, revision := nativeMFAHardeningChallenge(t, f.client, &session)
	f.sealerA.drain()
	mark := f.sealerA.mark()
	f.sealerA.hold()
	defer f.sealerA.release()
	verify := startVerify(t, f.client, &session, challengeID, revision, "totp", wrong)
	f.sealerA.waitEntered(t)
	create := make(chan int, 1)
	go func() {
		status, _, _ := nativeMFAHardeningChallengeBudget(t, f.client, &session)
		create <- status
	}()
	select {
	case status := <-create:
		t.Fatalf("the replacement challenge finished while the verification held the lock: %d", status)
	case <-time.After(300 * time.Millisecond):
	}
	f.sealerA.release()
	outcome := awaitVerify(t, verify)
	if outcome.status != http.StatusOK || mfaData(t, outcome.payload)["verified"] != false {
		t.Fatalf("in-flight verification = %d %s", outcome.status, outcome.body)
	}
	if status := <-create; status != http.StatusCreated {
		t.Fatalf("replacement challenge = %d, want 201", status)
	}
	if attempts := nativeMFAHardeningAttempts(t, f, challengeID); attempts != 1 {
		t.Fatalf("the executed failure was lost by the replacement: attempts=%d", attempts)
	}
	if counted := nativeMFAHardeningBudget(t, f, userID, "totp"); counted != 1 {
		t.Fatalf("the executed failure was lost by the replacement: failures=%d", counted)
	}

	// The opposite order: the replacement runs first and revokes the challenge, so
	// the verification is refused without opening the secret.
	revokedID, revokedRevision := nativeMFAHardeningChallenge(t, f.client, &session)
	if status, _, _ := nativeMFAHardeningChallengeBudget(t, f.client, &session); status != http.StatusCreated {
		t.Fatalf("replacement-first create = %d, want 201", status)
	}
	f.sealerA.drain()
	mark = f.sealerA.mark()
	if status, _, body := nativeMFAHardeningVerify(t, f.client, &session, revokedID, revokedRevision, "totp", mfaCurrentCode(t, secret)); status != http.StatusConflict {
		t.Fatalf("revoked challenge = %d: %s", status, body)
	}
	if opened := f.sealerA.openedSince(mark); opened != 0 {
		t.Fatalf("a revoked challenge reached the comparison boundary (%d opens)", opened)
	}
}

// TestNativeStepUpCooldownBoundary proves the lock and window boundaries are the
// database's decision.
func TestNativeStepUpCooldownBoundary(t *testing.T) {
	f := newNativeMFAHardeningFixture(t)
	password := "mfa-hardening-cooldown-password"
	_, userID, session, secret := nativeMFAHardeningEnrolled(t, f, "mfa.cooldown.", password)

	// The lock is created first below, so a challenge that must be verifiable is
	// created while the budget is still clean.
	pendingID, pendingRevision := nativeMFAHardeningChallengeFor(t, f.client, &session, "mfa_reset")
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		INSERT INTO identity.step_up_failures
			(tenant_id, user_id, factor, failed_count, window_started_at, locked_until)
		VALUES ($1,$2,'totp',5, clock_timestamp() - interval '1 minute', clock_timestamp() + interval '5 minutes')
		ON CONFLICT (tenant_id,user_id,factor) DO UPDATE
		SET failed_count=5, window_started_at=clock_timestamp() - interval '1 minute',
		    locked_until=clock_timestamp() + interval '5 minutes'
	`, f.tenantA, userID)
	if status, _, body := nativeMFAHardeningVerify(t, f.client, &session, pendingID, pendingRevision, "totp", mfaCurrentCode(t, secret)); status != http.StatusTooManyRequests {
		t.Fatalf("active lock verify = %d: %s", status, body)
	}
	if status, _, _ := nativeMFAHardeningChallengeBudget(t, f.client, &session); status != http.StatusTooManyRequests {
		t.Fatalf("active lock create = %d, want 429", status)
	}

	// Cooldown expired together with the window: a clean slate, not a NULL lock.
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		UPDATE identity.step_up_failures
		SET failed_count=5, window_started_at=clock_timestamp() - interval '16 minutes',
		    locked_until=clock_timestamp() - interval '1 minute'
		WHERE tenant_id=$1 AND user_id=$2 AND factor='totp'
	`, f.tenantA, userID)
	f.sealerA.drain()
	windowID, windowRevision := nativeMFAHardeningChallenge(t, f.client, &session)
	if status, _, body := nativeMFAHardeningVerify(t, f.client, &session, windowID, windowRevision, "totp", nativeMFAHardeningWrongCode(t, secret)); status != http.StatusOK {
		t.Fatalf("expired cooldown verify = %d: %s", status, body)
	}
	if counted := nativeMFAHardeningBudget(t, f, userID, "totp"); counted != 1 {
		t.Fatalf("the new window failed_count = %d, want 1", counted)
	}

	// Defensive boundary: a hand-written row whose cooldown is shorter than the
	// window still fails closed while the window is open.
	shortLockID, shortLockRevision := nativeMFAHardeningChallengeFor(t, f.client, &session, "mfa_reset")
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		UPDATE identity.step_up_failures
		SET failed_count=5, window_started_at=clock_timestamp() - interval '1 minute',
		    locked_until=clock_timestamp() - interval '1 second'
		WHERE tenant_id=$1 AND user_id=$2 AND factor='totp'
	`, f.tenantA, userID)
	f.sealerA.drain()
	shortMark := f.sealerA.mark()
	if status, _, body := nativeMFAHardeningVerify(t, f.client, &session, shortLockID, shortLockRevision, "totp", mfaCurrentCode(t, secret)); status != http.StatusTooManyRequests {
		t.Fatalf("expired lock inside an open window = %d: %s", status, body)
	}
	if opened := f.sealerA.openedSince(shortMark); opened != 0 {
		t.Fatalf("a fail-closed budget window reached the comparison boundary (%d opens)", opened)
	}
}

// TestNativeMFAResetRacesPasswordChange proves a concurrent credential write and
// MFA reset leave exactly one version, one active hash and one dead password.
func TestNativeMFAResetRacesPasswordChange(t *testing.T) {
	f := newNativeMFAHardeningFixture(t)
	password := "mfa-hardening-original-password"
	rotated := "mfa-hardening-rotated-password"
	login := "mfa.race.change." + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	userID := nativeMFAHardeningAccount(t, f.ctx, f.bootstrapPool, f.tenantA, login, password)
	session, _ := mfaLoginWithCookie(t, f.client, login, password)
	admin := f.admin

	revision, credential := nativeMFAHardeningVersions(t, f, f.tenantA, userID)
	resetProof := nativeMFAHardeningResetProof(t, f, f.client, &admin, userID, revision)
	changeSession := session
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var changeStatus, resetStatus int
	var resetBody string
	go func() {
		defer wait.Done()
		<-start
		changeStatus, _, _ = f.client.do(t, http.MethodPost, "/api/v1/auth/password:change", map[string]any{
			"currentPassword": password, "newPassword": rotated,
			"expectedCredentialVersion": credential,
		}, &changeSession)
	}()
	go func() {
		defer wait.Done()
		<-start
		resetStatus, _, resetBody = nativeMFAHardeningResetRequest(t, f, &admin, userID, login, revision, credential, resetProof)
	}()
	close(start)
	wait.Wait()

	changeWon := changeStatus == http.StatusCreated
	resetWon := resetStatus == http.StatusOK
	if changeWon == resetWon {
		t.Fatalf("concurrent change/reset = %d/%d, want exactly one success: %s", changeStatus, resetStatus, resetBody)
	}
	if changeWon && resetStatus != http.StatusConflict {
		t.Fatalf("the losing reset = %d, want 409", resetStatus)
	}
	// A losing self password change may be refused by the transaction revision or
	// already by the revoked session it raced with; both are truthful refusals.
	if resetWon && changeStatus != http.StatusConflict && changeStatus != http.StatusUnauthorized {
		t.Fatalf("the losing password change = %d, want 409 or 401", changeStatus)
	}

	_, after := nativeMFAHardeningVersions(t, f, f.tenantA, userID)
	if after != credential+1 {
		t.Fatalf("credential version after the race = %d, want exactly %d", after, credential+1)
	}
	if active := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT count(*) FROM identity.credentials
		WHERE tenant_id=$1 AND user_id=$2 AND status='active'
	`, f.tenantA, userID); active != 1 {
		t.Fatalf("active credential rows = %d, want 1", active)
	}
	if activeAtVersion := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT count(*) FROM identity.credentials
		WHERE tenant_id=$1 AND user_id=$2 AND credential_version=$3 AND status='active'
	`, f.tenantA, userID, after); activeAtVersion != 1 {
		t.Fatalf("active rows at the final version = %d, want 1", activeAtVersion)
	}

	if changeWon {
		if status, _, _ := mfaAttemptLogin(t, f.client, login, rotated); status != http.StatusCreated {
			t.Fatalf("winner password login = %d, want 201", status)
		}
		if status, _, _ := mfaAttemptLogin(t, f.client, login, password); status != http.StatusUnauthorized {
			t.Fatalf("superseded password login = %d, want 401", status)
		}
		if state := mfaTenantString(t, f.ctx, f.apiPool, f.tenantA, `
			SELECT COALESCE(out_state,'') FROM identity.mfa_state($1,$2)
		`, f.tenantA, userID); state == "reset_required" {
			t.Fatalf("the losing reset still changed MFA state: %s", state)
		}
		return
	}
	// The reset won: the original hash survived at the new version and the
	// password change attempt must not have written anything.
	gated, _ := mfaLoginWithCookie(t, f.client, login, password)
	status, payload, body := f.client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &gated)
	if status != http.StatusOK {
		t.Fatalf("reset winner login status = %d: %s", status, body)
	}
	if mfaData(t, payload)["mustReenrollMfa"] != true {
		t.Fatal("the reset winner session is not gated")
	}
	if status, _, _ := mfaAttemptLogin(t, f.client, login, rotated); status != http.StatusUnauthorized {
		t.Fatalf("the losing password change left the new password usable = %d", status)
	}
}

// TestNativeMFABrokenAccountDiagnosticAndRecovery proves the runbook diagnostics
// are tenant-scoped and that the audited administrator password reset repairs a
// broken credential chain without any history backfill.
func TestNativeMFABrokenAccountDiagnosticAndRecovery(t *testing.T) {
	f := newNativeMFAHardeningFixture(t)
	password := "mfa-hardening-broken-password"
	replacement := "mfa-hardening-replacement-password"
	login := "mfa.broken." + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	userID := nativeMFAHardeningAccount(t, f.ctx, f.bootstrapPool, f.tenantA, login, password)

	// Simulate the pre-fix chain: the account version advanced, the credential row
	// stayed at the previous version and no row exists at the current one.
	nativeMFAHardeningOwnerExec(t, f, f.tenantA, `
		UPDATE identity.accounts
		SET credential_version = 2, mfa_reset_required = true,
		    session_version = session_version + 1, account_revision = account_revision + 1
		WHERE tenant_id=$1 AND user_id=$2
	`, f.tenantA, userID)

	diagnostic := `
		SELECT count(*) FROM identity.accounts a
		WHERE a.tenant_id = $1 AND a.user_id = $2
		  AND a.status <> 'archived'
		  AND NOT EXISTS (
		    SELECT 1 FROM identity.credentials c
		     WHERE c.tenant_id = a.tenant_id AND c.user_id = a.user_id
		       AND c.credential_version = a.credential_version
		       AND c.status = 'active')`
	if broken := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, diagnostic, f.tenantA, userID); broken != 1 {
		t.Fatalf("the tenant-scoped diagnostic found %d broken rows, want 1", broken)
	}
	// The same target user under another tenant context must be invisible.
	if foreign := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantB, diagnostic, f.tenantB, userID); foreign != 0 {
		t.Fatalf("the diagnostic exposed the target under another tenant: %d rows", foreign)
	}

	if status, _, _ := mfaAttemptLogin(t, f.client, login, password); status != http.StatusUnauthorized {
		t.Fatalf("broken account login = %d, want 401", status)
	}
	admin := f.admin
	revision, credential := nativeMFAHardeningVersions(t, f, f.tenantA, userID)
	if credential != 2 {
		t.Fatalf("broken credential version = %d, want 2", credential)
	}
	body := map[string]any{
		"initialPassword":           replacement,
		"expectedRevision":          revision,
		"expectedCredentialVersion": credential,
	}
	path := "/api/v1/admin/users/" + userID.String() + "/password:reset"
	nativeAdminWriteProof(t, f.ctx, f.apiPool, f.client, f.tenantA, &admin, f.adminSecret, http.MethodPost, path, body)
	status, payload, message := f.client.do(t, http.MethodPost, path, body, &admin)
	admin.stepUpChallenge = ""
	if status != http.StatusOK {
		t.Fatalf("administrator password reset = %d: %s", status, message)
	}
	reset := mfaData(t, payload)
	if reset["passwordState"] != "change_required" {
		t.Fatalf("reset password state = %v", reset["passwordState"])
	}
	if int(reset["credentialVersion"].(float64)) != credential+1 {
		t.Fatalf("reset credential version = %v, want %d", reset["credentialVersion"], credential+1)
	}
	if active := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, `
		SELECT count(*) FROM identity.credentials
		WHERE tenant_id=$1 AND user_id=$2 AND credential_version=$3 AND status='active'
	`, f.tenantA, userID, credential+1); active != 1 {
		t.Fatalf("active credentials after recovery = %d, want 1", active)
	}
	if repaired := mfaTenantInt(t, f.ctx, f.bootstrapPool, f.tenantA, diagnostic, f.tenantA, userID); repaired != 0 {
		t.Fatalf("the diagnostic still reports %d broken rows", repaired)
	}
	if status, _, _ := mfaAttemptLogin(t, f.client, login, password); status != http.StatusUnauthorized {
		t.Fatalf("the pre-reset password is still usable = %d", status)
	}
	gated, _ := mfaLoginWithCookie(t, f.client, login, replacement)
	status, payload, message = f.client.do(t, http.MethodGet, "/api/v1/auth/session", nil, &gated)
	if status != http.StatusOK {
		t.Fatalf("recovered account session = %d: %s", status, message)
	}
	if mfaData(t, payload)["mustReenrollMfa"] != true {
		t.Fatal("the recovered account skipped the MFA setup gate")
	}
}
