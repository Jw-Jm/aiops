package rca

import "testing"

func TestEveryRecipeRespectsSP04KernelDepth(t *testing.T) {
	for _, name := range []string{"dimm-failure", "pvc-csi-failure", "node-failure"} {
		r, err := Builtin(name)
		if err != nil || r.GraphDepth > 2 {
			t.Fatalf("%s cannot execute under SP04 graph admission: depth=%d err=%v", name, r.GraphDepth, err)
		}
	}
}
