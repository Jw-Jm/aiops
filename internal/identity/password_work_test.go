package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingKDF is a controllable stand-in for the Argon2id call. It lets the test
// occupy the real work gate without allocating 64 MiB per attempt, and it records
// how many KDF calls actually ran at the same time.
type blockingKDF struct {
	started chan struct{}
	release chan struct{}
	active  int32
	peak    int32
	calls   int32
	panicOn bool
}

func (k *blockingKDF) work(password, salt []byte, timeCost, memory uint32, threads uint8, keyLen uint32) []byte {
	if k.panicOn {
		panic("simulated kdf failure")
	}
	current := atomic.AddInt32(&k.active, 1)
	for {
		peak := atomic.LoadInt32(&k.peak)
		if current <= peak || atomic.CompareAndSwapInt32(&k.peak, peak, current) {
			break
		}
	}
	atomic.AddInt32(&k.calls, 1)
	k.started <- struct{}{}
	<-k.release
	atomic.AddInt32(&k.active, -1)
	key := make([]byte, keyLen)
	copy(key, salt)
	return key
}

func (k *blockingKDF) concurrentPeak() int { return int(atomic.LoadInt32(&k.peak)) }
func (k *blockingKDF) callCount() int      { return int(atomic.LoadInt32(&k.calls)) }

// installPasswordWork replaces the process-wide gate and KDF for one test and
// restores both afterwards.
func installPasswordWork(t *testing.T, limit int) *blockingKDF {
	t.Helper()
	originalGate, originalKDF := passwordWork, passwordKDF
	t.Cleanup(func() {
		passwordWork = originalGate
		passwordKDF = originalKDF
	})
	fake := &blockingKDF{started: make(chan struct{}, 16), release: make(chan struct{})}
	passwordWork = newPasswordWorkGate(limit)
	passwordKDF = fake.work
	return fake
}

// validEncodedHash builds a syntactically valid hash without running the real
// KDF, so verification can reach the gated work step.
func validEncodedHash(t *testing.T) string {
	t.Helper()
	salt := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef"))
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	return "argon2id$v=19$m=65536,t=2,p=1$" + salt + "$" + key
}

// TestPasswordWorkIsSharedAndBounded proves that hashing and verification share
// one process-wide limit, that a caller above the limit is rejected instead of
// queueing, and that a finished call returns its slot.
func TestPasswordWorkIsSharedAndBounded(t *testing.T) {
	const limit = 2
	fake := installPasswordWork(t, limit)
	encoded := validEncodedHash(t)
	password := strings.Repeat("bounded-work-", 2)

	var group sync.WaitGroup
	errs := make(chan error, 2)
	group.Add(2)
	go func() {
		defer group.Done()
		_, err := HashPasswordContext(context.Background(), password)
		errs <- err
	}()
	<-fake.started
	go func() {
		defer group.Done()
		_, err := VerifyPasswordContext(context.Background(), password, encoded)
		errs <- err
	}()
	<-fake.started

	// Every slot is occupied: the third caller must fail fast, not wait.
	start := time.Now()
	if _, err := HashPasswordContext(context.Background(), password); !errors.Is(err, ErrPasswordWorkBusy) {
		t.Fatalf("third caller error = %v, want ErrPasswordWorkBusy", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("busy rejection took %s; it must not queue", elapsed)
	}
	if calls := fake.callCount(); calls != 2 {
		t.Fatalf("KDF calls while saturated = %d, want 2", calls)
	}

	close(fake.release)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("occupied caller failed: %v", err)
		}
	}
	if peak := fake.concurrentPeak(); peak > limit {
		t.Fatalf("concurrent KDF peak = %d, want <= %d", peak, limit)
	}
	// Slots came back: a further caller runs again.
	if _, err := HashPasswordContext(context.Background(), password); err != nil {
		t.Fatalf("caller after release failed: %v", err)
	}
	if calls := fake.callCount(); calls != 3 {
		t.Fatalf("KDF calls after release = %d, want 3", calls)
	}
}

// TestPasswordWorkCancellationBeforeStart proves a cancelled request never starts
// the KDF and never takes a slot.
func TestPasswordWorkCancellationBeforeStart(t *testing.T) {
	fake := installPasswordWork(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := HashPasswordContext(ctx, strings.Repeat("cancelled-work-", 2)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled hashing error = %v, want context.Canceled", err)
	}
	if calls := fake.callCount(); calls != 0 {
		t.Fatalf("cancelled request still ran the KDF %d time(s)", calls)
	}
	if len(passwordWork.slots) != 0 {
		t.Fatalf("cancelled request leaked a slot: %d occupied", len(passwordWork.slots))
	}
}

// TestPasswordWorkSlotHeldUntilKDFReturns proves that an HTTP cancellation after
// the work started does not free the memory slot early: the next caller stays
// rejected until the Argon2 call actually returned.
func TestPasswordWorkSlotHeldUntilKDFReturns(t *testing.T) {
	fake := installPasswordWork(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := HashPasswordContext(ctx, strings.Repeat("in-flight-work-", 2))
		done <- err
	}()
	<-fake.started
	cancel()
	if _, err := HashPasswordContext(context.Background(), strings.Repeat("in-flight-work-", 2)); !errors.Is(err, ErrPasswordWorkBusy) {
		t.Fatalf("caller during cancellation error = %v, want ErrPasswordWorkBusy", err)
	}
	close(fake.release)
	if err := <-done; err != nil {
		t.Fatalf("started work must finish after cancellation, got %v", err)
	}
	if _, err := HashPasswordContext(context.Background(), strings.Repeat("in-flight-work-", 2)); err != nil {
		t.Fatalf("slot was not returned after the KDF returned: %v", err)
	}
}

// TestPasswordWorkSlotReturnedOnKDFPanic covers the abnormal-exit path: the defer
// must release the slot even if the work function panics.
func TestPasswordWorkSlotReturnedOnKDFPanic(t *testing.T) {
	fake := installPasswordWork(t, 1)
	fake.panicOn = true
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected the simulated KDF panic")
			}
		}()
		_, _ = HashPasswordContext(context.Background(), strings.Repeat("panicking-work-", 2))
	}()
	if len(passwordWork.slots) != 0 {
		t.Fatalf("panic leaked a password work slot: %d occupied", len(passwordWork.slots))
	}
	passwordKDF = func(password, salt []byte, timeCost, memory uint32, threads uint8, keyLen uint32) []byte {
		return make([]byte, keyLen)
	}
	if _, err := HashPasswordContext(context.Background(), strings.Repeat("panicking-work-", 2)); err != nil {
		t.Fatalf("work after a panic failed: %v", err)
	}
}

// TestPasswordWorkLimitIsDeliberatelySmall records the reviewed default: two
// Argon2id workspaces (~128 MiB) fit the default 512 MiB API container.
func TestPasswordWorkLimitIsDeliberatelySmall(t *testing.T) {
	if passwordWorkLimit != 2 {
		t.Fatalf("passwordWorkLimit = %d, want the reviewed value 2", passwordWorkLimit)
	}
	if capacity := cap(newPasswordWorkGate(passwordWorkLimit).slots); capacity != passwordWorkLimit {
		t.Fatalf("gate capacity = %d, want %d", capacity, passwordWorkLimit)
	}
}
