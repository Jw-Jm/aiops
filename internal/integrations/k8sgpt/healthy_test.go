package k8sgpt

import "testing"

func TestLockedUpstreamHealthyNullResultsAreValid(t *testing.T) {
	out, err := Decode([]byte(`{"provider":"","errors":null,"status":"OK","problems":0,"results":null}`))
	if err != nil || len(out.Results) != 0 {
		t.Fatalf("official healthy result rejected: %+v %v", out, err)
	}
}
