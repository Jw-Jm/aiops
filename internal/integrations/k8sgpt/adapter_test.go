package k8sgpt

import "testing"

func TestOutputRequiresCompleteConsistentNoLLMEnvelope(t *testing.T) {
	for _, raw := range []string{
		`{"status":"OK","results":[]}`,
		`{"status":"ProblemDetected","problems":0,"provider":"","errors":[],"results":[]}`,
		`{"status":"OK","problems":1,"provider":"","errors":[],"results":[{"kind":"Pod","name":"apps/a","error":[{"Text":"issue"}]}]}`,
		`{"status":"ProblemDetected","problems":2,"provider":"","errors":[],"results":[{"kind":"Pod","name":"apps/a","error":[{"Text":"issue"}]}]}`,
		`{"status":"ProblemDetected","problems":1,"provider":"","errors":[],"results":[{"kind":"Pod","name":"apps/a","error":[{"Text":""}]}]}`,
	} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatalf("format drift accepted: %s", raw)
		}
	}
}

func TestOfficialUpstreamNoLLMOutputShape(t *testing.T) {
	raw := []byte(`{"status":"ProblemDetected","problems":1,"provider":"","errors":null,"results":[{"kind":"Pod","name":"fixture/unschedulable","error":[{"Text":"fixture: insufficient memory","KubernetesDoc":"","Sensitive":null}],"details":"","parentObject":""}]}`)
	out, err := Decode(raw)
	if err != nil || len(out.Results) != 1 {
		t.Fatalf("locked output rejected: %v", err)
	}
}

func TestLockedBinaryArchitectureIdentitiesAreFinite(t *testing.T) {
	if LockedBinaryForArchitecture("arm64") != LockedBinarySHA256 || LockedBinaryForArchitecture("amd64") != LockedBinarySHA256AMD64 || LockedBinarySHA256 == LockedBinarySHA256AMD64 {
		t.Fatal("native binary identity selection drifted")
	}
	for _, target := range []string{"", "386", "aarch64", "linux/amd64", "riscv64"} {
		if LockedBinaryForArchitecture(target) != "" {
			t.Fatalf("unsupported target inherited executable permission: %s", target)
		}
	}
}
