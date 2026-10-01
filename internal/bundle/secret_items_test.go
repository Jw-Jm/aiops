package bundle

import (
	"context"
	"ops-platform/internal/profile"
	"strings"
	"testing"
)

func TestInstallerChecksSelectedSecretVolumeKeys(t *testing.T) {
	volume := map[string]any{"secret": map[string]any{"secretName": "ops-platform-runtime", "items": []any{map[string]any{"key": "archiveTenantCredentials", "path": "tenants.json"}}}}
	checked := false
	err := checkSecretReferences(context.Background(), volume, profile.ResolvedProfile{}, func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "archiveTenantCredentials") {
			checked = true
			return nil, nil
		}
		return []byte("ops-platform-runtime"), nil
	})
	if !checked || err == nil {
		t.Fatal("missing selected Secret key was not rejected before import")
	}
	err = checkSecretReferences(context.Background(), volume, profile.ResolvedProfile{}, func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "go-template=") {
			return []byte("present"), nil
		}
		return []byte("ops-platform-runtime"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
