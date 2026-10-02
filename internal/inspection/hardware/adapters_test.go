package hardware

import (
	"math"
	"testing"
)

func TestCorootCheckAndNPDQualifiedSemantics(t *testing.T) {
	var value float32 = 10
	if got := CheckThreshold("cpu", &value, 10); got.Status != "normal" {
		t.Fatal(got)
	}
	value = 10.1
	if got := CheckThreshold("cpu", &value, 10); got.Status != "degraded" {
		t.Fatal(got)
	}
	value = float32(math.NaN())
	if got := CheckThreshold("cpu", &value, 10); got.Status != "unknown" || !got.Degraded {
		t.Fatal(got)
	}
	line := "Buffer I/O error on dev sda"
	got, err := MatchKernelLog(&line)
	if err != nil || len(got) != 1 || got[0].RuleID != "IOError" {
		t.Fatalf("%+v %v", got, err)
	}
	if got, _ := MatchKernelLog(nil); got[0].Status != "unknown" {
		t.Fatal(got)
	}
}
