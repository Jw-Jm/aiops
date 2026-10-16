package investigation

import (
	"ops-platform/internal/modelprofile"
	"testing"
)

func TestModelReservationUsesFrozenProfile(t *testing.T) {
	for _, window := range []int{8192, 16384, 32768, 65536} {
		p := modelprofile.Profile{Version: "test/v1", ContextWindow: window, OutputTokenLimit: 2048}
		r := Repository{AssistantModelProfile: &p}
		j := Job{SchemaVersion: "investigation-job/v3", Assistant: &AssistantTask{ModelProfileVersion: p.Reference()}}
		usage := Usage{ModelRequests: 1, InputTokens: int64(window), OutputTokens: 2048, ResultBytes: 65536}
		if err := r.validateModelReservation(j, usage); err != nil {
			t.Fatal(window, err)
		}
		usage.InputTokens++
		if err := r.validateModelReservation(j, usage); err != ErrBudget {
			t.Fatal("forged bound accepted", window, err)
		}
		usage.InputTokens--
		usage.OutputTokens++
		if err := r.validateModelReservation(j, usage); err != ErrBudget {
			t.Fatal("output cap ignored", err)
		}
		j.Assistant.ModelProfileVersion = "other"
		if err := r.validateModelReservation(j, usage); err != ErrDenied {
			t.Fatal("profile binding ignored", err)
		}
	}
	legacy := Job{SchemaVersion: "investigation-job/v2"}
	u := Usage{ModelRequests: 1, InputTokens: 16384, OutputTokens: 8192, ResultBytes: 65536}
	if err := (Repository{}).validateModelReservation(legacy, u); err != nil {
		t.Fatal(err)
	}
	u.InputTokens = 32768
	if err := (Repository{}).validateModelReservation(legacy, u); err != ErrBudget {
		t.Fatal("legacy bounds changed", err)
	}
	if err := (Repository{}).validateModelReservation(Job{SchemaVersion: "investigation-job/v3"}, u); err != ErrDenied {
		t.Fatal("missing profile accepted", err)
	}
}
