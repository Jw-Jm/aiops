package action

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestAuthorityNativeForRecheckRefusesUnknownOrMixedSources(t *testing.T) {
	user := uuid.Must(uuid.NewV7())
	cases := []struct {
		name      string
		authority Authority
		native    bool
		denied    bool
	}{
		{"native complete", Authority{Source: "native", UserID: user, AuthorizationVersion: 2}, true, false},
		{"legacy explicit", Authority{Source: "legacy"}, false, false},
		{"historical empty", Authority{}, false, false},
		{"native without user", Authority{Source: "native", AuthorizationVersion: 2}, false, true},
		{"native without version", Authority{Source: "native", UserID: user}, false, true},
		{"legacy carrying user", Authority{Source: "legacy", UserID: user}, false, true},
		{"legacy carrying version", Authority{Source: "legacy", AuthorizationVersion: 3}, false, true},
		{"unknown source", Authority{Source: "oidc"}, false, true},
		{"unknown source with identity", Authority{Source: "oidc", UserID: user, AuthorizationVersion: 1}, false, true},
	}
	for _, test := range cases {
		native, err := test.authority.nativeForRecheck()
		if test.denied {
			if !errors.Is(err, ErrDenied) {
				t.Fatalf("%s: error = %v", test.name, err)
			}
			continue
		}
		if err != nil || native != test.native {
			t.Fatalf("%s: native=%v err=%v", test.name, native, err)
		}
	}
}

func TestCommandAuthorityFromContextDefaultsToLegacy(t *testing.T) {
	if ref := commandAuthorityFromContext(t.Context()); ref.Source != "legacy" || ref.UserID != uuid.Nil || ref.AuthorizationVersion != 0 {
		t.Fatalf("context without a native session = %+v", ref)
	}
}
