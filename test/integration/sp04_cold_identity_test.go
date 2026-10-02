package integration

import "testing"

func TestSP04ColdImportRequiresPositiveSignedImageIdentity(t *testing.T) {
	signed := "ops.local/task27/platform-worker@sha256:locked"
	ownDigest := "ops.local/owned-current/platform-worker@sha256:locked"
	ownTag := "ops.local/owned-current/platform-worker:1.0.0"
	for _, tc := range []struct {
		name     string
		identity sp04ColdImageIdentity
		want     bool
	}{
		{"signed-immutable", sp04ColdImageIdentity{ID: "sha256:id", RepoDigests: []string{signed}}, true},
		{"own-preparation", sp04ColdImageIdentity{ID: "sha256:id", RepoDigests: []string{ownDigest}, RepoTags: []string{ownTag}}, true},
		{"replaced-mutable-tag-no-digest", sp04ColdImageIdentity{ID: "sha256:replacement", RepoTags: []string{ownTag}}, false},
		{"shared-repository", sp04ColdImageIdentity{ID: "sha256:id", RepoDigests: []string{signed, "other@sha256:locked"}}, false},
		{"shared-tag", sp04ColdImageIdentity{ID: "sha256:id", RepoDigests: []string{signed}, RepoTags: []string{"other:latest"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sp04ColdIdentityMatches(tc.identity, signed, ownDigest, ownTag); got != tc.want {
				t.Fatalf("identity proof got %v want %v", got, tc.want)
			}
		})
	}
}
