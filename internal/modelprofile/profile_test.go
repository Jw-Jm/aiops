package modelprofile

import (
	"encoding/json"
	"os"
	"testing"
)

func fixture(t *testing.T) Profile {
	t.Helper()
	b, err := os.ReadFile("../../test/fixtures/assistant/model-profile.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var p Profile
	if json.Unmarshal(b, &p) != nil {
		t.Fatal("invalid fixture")
	}
	return p
}
func TestProfileRequiresAdmissionAndBounds(t *testing.T) {
	p := fixture(t)
	if !p.Valid("127.0.0.1:8083") || p.Valid(":8083") || p.Valid("0.0.0.0:8083") {
		t.Fatal("fixture escaped loopback")
	}
	reference := p.Reference()
	p.ContextWindow = 32768
	if reference == p.Reference() {
		t.Fatal("changed capability retained reference")
	}
	p.AdmissionState = "pending"
	if p.Valid("127.0.0.1:8083") {
		t.Fatal("pending profile admitted")
	}
	p.AdmissionState = "admitted"
	p.BaseURL = "https://example.com/v1"
	if p.Valid(":8083") {
		t.Fatal("external model accepted")
	}
	p.BaseURL = "http://127.0.0.1:11434/v1"
	p.OutputTokenLimit = p.ContextWindow
	if p.Valid(":8083") {
		t.Fatal("output consumes entire context")
	}
	p = fixture(t)
	p.TaskKinds = []string{"data_query", "data_query"}
	if p.Valid("127.0.0.1:8083") {
		t.Fatal("duplicate tasks accepted")
	}
}
func TestProfileCanonicalReference(t *testing.T) {
	p := fixture(t)
	// Same vector is asserted in the investigator's native runtime test.
	if p.Reference() != "assistant-smoke/v1@sha256:ef4cbe7d6c2dd7bfa40965887891c8f17a234a4689961fe8964391ec7271dd66" {
		t.Fatal("canonical digest changed")
	}
}

func TestProfileMillionTokenContextBoundary(t *testing.T) {
	p := fixture(t)
	p.ContextWindow = 1000000
	if !p.Valid("127.0.0.1:8083") {
		t.Fatal("DeepSeek context rejected")
	}
	p.ContextWindow++
	if p.Valid("127.0.0.1:8083") {
		t.Fatal("unbounded context accepted")
	}
}

func TestHostedProfileIsExplicitAndPendingCannotRun(t *testing.T) {
	raw, err := os.ReadFile("../../test/fixtures/assistant/model-profile-hosted.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var p Profile
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Valid(":8083") {
		t.Fatal("pending hosted profile ran")
	}
	p.AdmissionState = "admitted"
	if !p.Valid(":8083") {
		t.Fatal("explicit admitted hosted profile rejected")
	}
	for _, mutate := range []func(*Profile){
		func(p *Profile) { p.BaseURL = "http://api.deepseek.com/v1" },
		func(p *Profile) { p.BaseURL = "https://api.deepseek.com.evil.test/v1" },
		func(p *Profile) { p.BaseURL = "https://api.deepseek.com:443/v1" },
		func(p *Profile) { p.ModelID = "unknown" },
		func(p *Profile) { p.CostPolicy = "zero-charge" },
		func(p *Profile) { p.Hosted = nil },
		func(p *Profile) {
			p.WeightsDigest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
		},
		func(p *Profile) { p.AdmissionState = "fixture_only" },
		func(p *Profile) { p.SchemaVersion = "assistant-model-profile/v1" },
	} {
		bad := p
		mutate(&bad)
		if bad.Valid(":8083") {
			t.Fatalf("invalid hosted profile accepted: %s %s %s", bad.SchemaVersion, bad.BaseURL, bad.CostPolicy)
		}
	}
	before := p.Reference()
	p.Hosted = &HostedEvidence{Provider: "deepseek"}
	if before == p.Reference() {
		t.Fatal("hosted evidence omitted from frozen reference")
	}
}
