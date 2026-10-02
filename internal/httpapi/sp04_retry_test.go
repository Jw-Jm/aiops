package httpapi

import (
	"context"
	"errors"
	"testing"
)

func TestGenerationRetryOnlyForUnpagedRead(t *testing.T) {
	for _, paged := range []bool{false, true} {
		calls := 0
		data, err := generationRead(context.Background(), paged, func() ([]byte, error) {
			calls++
			if calls == 1 {
				return nil, callError{409, "STALE_CONTEXT"}
			}
			return []byte("current"), nil
		})
		if paged {
			if calls != 1 || err == nil {
				t.Fatal("paged cursor retried")
			}
		} else if calls != 2 || err != nil || string(data) != "current" {
			t.Fatal("fresh generation did not retry")
		}
	}
	calls := 0
	_, err := generationRead(context.Background(), false, func() ([]byte, error) { calls++; return nil, errors.New("transport") })
	if err == nil || calls != 1 {
		t.Fatal("unrelated error retried")
	}
}
