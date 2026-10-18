package action

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestExecutionRelationRequiresSameFaultIdentityAndTerminalParent(t *testing.T) {
	completed := time.Now()
	parent := Execution{ID: uuid.New(), State: "succeeded", CompletedAt: &completed, Binding: Binding{TenantID: uuid.New(), IncidentID: uuid.New(), Target: "target", TargetUID: "uid", ClusterUID: "cluster"}}
	binding := parent.Binding
	binding.RelatedExecutionID, binding.ExecutionRelation = &parent.ID, "follow_up"
	if err := validateRelatedExecution(binding, parent); err != nil {
		t.Fatal(err)
	}
	binding.ExecutionRelation = "rollback"
	if err := validateRelatedExecution(binding, parent); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Binding, *Execution)
		want   error
	}{
		{"other_tenant", func(b *Binding, _ *Execution) { b.TenantID = uuid.New() }, ErrDenied},
		{"other_incident", func(b *Binding, _ *Execution) { b.IncidentID = uuid.New() }, ErrDenied},
		{"other_target", func(b *Binding, _ *Execution) { b.Target = "other" }, ErrDenied},
		{"recreated_identity", func(b *Binding, _ *Execution) { b.TargetUID = "new-uid" }, ErrDenied},
		{"unknown_identity", func(b *Binding, _ *Execution) { b.TargetUID = "" }, ErrDenied},
		{"other_namespace", func(b *Binding, _ *Execution) { s := "restricted"; b.Namespace = &s }, ErrDenied},
		{"other_parent", func(_ *Binding, e *Execution) { e.ID = uuid.New() }, ErrDenied},
		{"running", func(_ *Binding, e *Execution) { e.State = "running" }, ErrConflict},
		{"unknown_side_effects", func(_ *Binding, e *Execution) { e.State = "execution_unknown" }, ErrConflict},
		{"missing_completion", func(_ *Binding, e *Execution) { e.CompletedAt = nil }, ErrConflict},
		{"unknown_relation", func(b *Binding, _ *Execution) { b.ExecutionRelation = "retry" }, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, e := binding, parent
			tc.change(&b, &e)
			if err := validateRelatedExecution(b, e); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestExecutionRelationChangesConfirmationButPreservesUnlinkedWireShape(t *testing.T) {
	b := Binding{DigestVersion: "execution-request/v1", TenantID: uuid.New(), IncidentID: uuid.New()}
	var old map[string]any
	if json.Unmarshal(Canonical(b), &old) != nil {
		t.Fatal("fixture")
	}
	for _, key := range []string{"relatedExecutionId", "executionRelation"} {
		if _, ok := old[key]; ok {
			t.Fatalf("unlinked legacy binding acquired %s", key)
		}
	}
	digest := b.Digest()
	id := uuid.New()
	b.RelatedExecutionID = &id
	b.ExecutionRelation = "follow_up"
	b.DigestVersion = "execution-request/v2"
	if b.Digest() == digest {
		t.Fatal("parent not included in confirmation")
	}
	digest = b.Digest()
	b.ExecutionRelation = "rollback"
	if b.Digest() == digest {
		t.Fatal("relation not included in confirmation")
	}
	digest = b.Digest()
	id2 := uuid.New()
	b.RelatedExecutionID = &id2
	if b.Digest() == digest {
		t.Fatal("parent substitution not included in confirmation")
	}
	zero := uuid.Nil
	for _, c := range []struct {
		id       *uuid.UUID
		relation string
		valid    bool
	}{{nil, "", true}, {nil, "follow_up", false}, {&zero, "follow_up", false}, {&id, "", false}, {&id, "rollback", true}} {
		if validExecutionRelation(c.id, c.relation) != c.valid {
			t.Fatal(c)
		}
	}
}
