package security

import (
	"github.com/google/uuid"
	"ops-platform/internal/action"
	"strings"
	"testing"
)

func TestExecutionProfilesSP07RejectUnsafeBoundaries(t *testing.T) {
	p := action.Profile{SchemaVersion: "execution-profile/v2", ID: uuid.New(), Version: 1, Name: "namespace", Type: "k8s_namespace", AllowedTargets: []string{"k8s+v1://tenant/cluster/core/Pod/pod"}, ClusterUID: "cluster", Namespace: "target", ToolImageDigest: "local/tool@sha256:" + strings.Repeat("a", 64), Tools: []string{"bash", "kubectl"}, NetworkPolicyRef: "target", CredentialRef: "serviceaccount:/target/command", TimeoutSeconds: 900, MaxOutputBytes: 10 << 20}
	if p.Validate() != nil {
		t.Fatal("valid namespace profile rejected")
	}
	for _, change := range []func(*action.Profile){func(p *action.Profile) { p.ToolImageDigest = "local/tool:latest" }, func(p *action.Profile) { p.Type = "unknown" }, func(p *action.Profile) { p.Namespace = "" }, func(p *action.Profile) { p.TimeoutSeconds = 901 }, func(p *action.Profile) { p.MaxOutputBytes = 10<<20 + 1 }, func(p *action.Profile) { p.AllowedTargets = []string{"k8s+v1://tenant/other/core/Pod/pod"} }, func(p *action.Profile) { p.Tools = []string{"virtctl"} }, func(p *action.Profile) { p.DevelopmentFallback = true }} {
		bad := p
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("unsafe profile accepted")
		}
	}
	for _, kind := range []string{"k8s_cluster", "ssh_user", "ssh_root"} {
		next := p
		next.Type = kind
		next.Namespace = ""
		if kind == "ssh_user" {
			next.Principal = "opsordinary"
			next.HostOnboardingRef = "native-report"
		}
		if kind == "ssh_root" {
			next.Principal = "root"
			next.HostOnboardingRef = "native-report"
		}
		if next.Validate() != nil || next.HighPrivilege() != (kind != "ssh_user") {
			t.Fatal("profile privilege classification", kind)
		}
	}
	if p.Allows("k8s+v1://tenant/other/core/Pod/pod", action.Options{TimeoutSeconds: 900, MaxOutputBytes: 10 << 20}) {
		t.Fatal("target substituted")
	}
}
func TestSP07ConfirmationDigestBindsEveryAuthorityField(t *testing.T) {
	b := action.Binding{DigestVersion: "execution-request/v1", TenantID: uuid.New(), Subject: "operator", IncidentID: uuid.New(), Target: "target", TargetUID: "uid", ClusterUID: "cluster", Shell: "bash", CommandDigest: action.Digest([]byte("true")), ProfileID: uuid.New(), ProfileVersion: 1, PolicyVersion: "policy", RiskVersion: action.RiskVersion, Options: action.Options{TimeoutSeconds: 900, MaxOutputBytes: 10 << 20}}
	for _, change := range []func(*action.Binding){func(b *action.Binding) { b.TenantID = uuid.New() }, func(b *action.Binding) { b.Subject = "agent" }, func(b *action.Binding) { b.IncidentID = uuid.New() }, func(b *action.Binding) { id := uuid.New(); b.ActionPlanID = &id }, func(b *action.Binding) { b.Target = "other" }, func(b *action.Binding) { b.TargetUID = "recreated" }, func(b *action.Binding) { b.ClusterUID = "other" }, func(b *action.Binding) { ns := "other"; b.Namespace = &ns }, func(b *action.Binding) { b.CommandDigest = action.Digest([]byte("true ")) }, func(b *action.Binding) { b.ProfileVersion++ }, func(b *action.Binding) { b.Options.TimeoutSeconds-- }, func(b *action.Binding) { b.PolicyVersion = "new" }, func(b *action.Binding) { b.RiskVersion = "new" }} {
		changed := b
		change(&changed)
		if b.Digest() == changed.Digest() {
			t.Fatal("authority substitution preserved digest")
		}
	}
}
