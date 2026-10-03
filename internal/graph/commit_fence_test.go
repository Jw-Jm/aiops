package graph

import (
	"context"
	"errors"
	"testing"
)

func TestCommitFenceRejectsChangedGenerationAndNewDegradation(t *testing.T) {
	g, q := semanticGraph(t)
	out, err := g.Query(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	g.SetSourceDegraded("required-source", "unavailable")
	err = g.WithCurrentResponse(t.Context(), out, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, ErrStale) || called {
		t.Fatalf("confirmed commit crossed new source degradation: %v called=%t", err, called)
	}
	g.SetSourceDegraded("required-source", "")
	out.GraphRevision.GraphGeneration++
	err = g.WithCurrentResponse(t.Context(), out, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, ErrStale) || called {
		t.Fatalf("confirmed commit crossed generation change: %v called=%t", err, called)
	}
}
