package httpapi

import (
	"testing"
	"time"

	"ops-platform/internal/identity"
)

func TestNativeSessionViewsUseServerOwnedAccountNames(t *testing.T) {
	session := identity.ActiveSession{Subject: "user:opaque-id", LoginName: "login.name", DisplayName: "显示姓名", Mode: "full"}
	for _, view := range []map[string]any{
		nativeActiveSessionView(session, "csrf", time.Now(), identity.AdministratorState{}, nil),
		nativeIssuedSessionView(issuedFromActive(session, "private-session-token", "csrf"), "csrf", time.Now(), identity.AdministratorState{}, nil),
	} {
		if view["loginName"] != session.LoginName || view["displayName"] != session.DisplayName || view["subject"] != session.Subject {
			t.Fatal("session view lost the server-owned names or replaced the opaque subject")
		}
		if _, exposed := view["sessionToken"]; exposed {
			t.Fatal("session token exposed in account view")
		}
	}
}
