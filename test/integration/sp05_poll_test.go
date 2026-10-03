package integration

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/finding"
	"testing"
	"time"
)

func TestSP05PollBirthClockSurvivesPostgreSQLPrecisionAndRestart(t *testing.T) {
	ctx, _, pool, b := sp05Database(t)
	s := finding.Service{Pool: pool}
	observed := time.Date(2026, 10, 2, 0, 0, 0, 123456789, time.UTC)
	first, birth, err := s.PollIdentity(ctx, b, "node-unready", "firing", "", observed)
	if err != nil {
		t.Fatal(err)
	}
	again, stored, err := s.PollIdentity(ctx, b, "node-unready", "firing", "", observed.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if first != again || !birth.Equal(stored) {
		t.Fatalf("poll identity drift after durable reload: id=%t birth=%s stored=%s", first == again, birth, stored)
	}
}

func TestSP05PollCrashAfterResolveAndLateReplayCannotCreateOccurrence(t *testing.T) {
	for _, mode := range []string{"crash-before-close", "late-replay"} {
		t.Run(mode, func(t *testing.T) {
			ctx, _, pool, b := sp05Database(t)
			s := finding.Service{Pool: pool}
			clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
			first, start, err := s.PollIdentity(ctx, b, "node-unready", "firing", "", clock)
			if err != nil {
				t.Fatal(err)
			}
			e := sp05Envelope(b, "poll-firing", first)
			e.StartsAt = start
			if _, _, err := s.Ingest(ctx, b, e); err != nil {
				t.Fatal(err)
			}
			e.EventID, e.IdempotencyKey, e.State, e.SourceSequence, e.ObservedAt = "poll-resolved", "poll-resolved", "resolved", 2, clock.Add(time.Second)
			if _, _, err := s.Ingest(ctx, b, e); err != nil {
				t.Fatal(err)
			}
			if mode == "late-replay" {
				if err := s.ClosePoll(ctx, b, "node-unready", first); err != nil {
					t.Fatal(err)
				}
				_, _, err := s.PollIdentity(ctx, b, "node-unready", "firing", "", clock)
				if !errors.Is(err, pgx.ErrNoRows) {
					t.Fatalf("late firing allocated a new occurrence: %v", err)
				}
			} else {
				next, _, err := s.PollIdentity(ctx, b, "node-unready", "firing", "", clock.Add(2*time.Second))
				if err != nil || next == first {
					t.Fatalf("crash between resolve and poll close lost new firing: same=%t err=%v", next == first, err)
				}
			}
		})
	}
}

func TestSP05FirstHealthyPollDurablyFencesLateAndRepeatedFiring(t *testing.T) {
	ctx, _, pool, b := sp05Database(t)
	s := finding.Service{Pool: pool}
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for _, observation := range []time.Time{clock, clock.Add(time.Minute)} {
		_, _, err := s.PollIdentity(ctx, b, "first-healthy", "resolved", "", observation)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("first/repeated healthy poll unexpectedly emits a Finding: %v", err)
		}
	}
	for _, late := range []time.Time{clock.Add(-time.Second), clock, clock.Add(30 * time.Second), clock.Add(time.Minute)} {
		if _, _, err := s.PollIdentity(ctx, b, "first-healthy", "firing", "", late); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("late observation %s reopened healthy signal: %v", late, err)
		}
	}
	if _, _, err := s.PollIdentity(ctx, b, "first-healthy", "firing", "", clock.Add(2*time.Minute)); err != nil {
		t.Fatalf("newer actual fault lost: %v", err)
	}
}
