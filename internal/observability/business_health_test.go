package observability

import (
	"strings"
	"testing"
)

// The business health of a collection source is registered explicitly and
// aggregated with "any". A source that recovers never clears the failure of
// another source, and an unregistered key is refused.
func TestBusinessHealthAggregationAndRegistration(t *testing.T) {
	metrics := NewMetrics()
	if metrics.BusinessDegraded() {
		t.Fatal("a fresh runtime is not degraded")
	}
	if metrics.RegisterBusinessComponent("sp05/tenant-a/cluster-a") != true {
		t.Fatal("a stable source key was refused")
	}
	if metrics.RegisterBusinessComponent("sp05/tenant-a/cluster-b") != true {
		t.Fatal("a stable source key was refused")
	}
	// A free label is not a source identity.
	if metrics.RegisterBusinessComponent("Free Label With Spaces") {
		t.Fatal("an arbitrary label was accepted as a component")
	}
	metrics.SetBusinessDegraded("sp05/tenant-a/cluster-a", true)
	if !metrics.BusinessDegraded() {
		t.Fatal("the degraded source was not reported")
	}
	// An unregistered key cannot create a failure: the runtime recovers as soon
	// as the registered source recovers.
	metrics.SetBusinessDegraded("sp05/tenant-a/cluster-c", true)
	metrics.SetBusinessDegraded("sp05/tenant-a/cluster-a", false)
	if metrics.BusinessDegraded() {
		t.Fatal("an unregistered key created a failure")
	}
	// One source recovers, the other stays failed.
	metrics.SetBusinessDegraded("sp05/tenant-a/cluster-a", true)
	metrics.SetBusinessDegraded("sp05/tenant-a/cluster-b", true)
	metrics.SetBusinessDegraded("sp05/tenant-a/cluster-a", false)
	if !metrics.BusinessDegraded() {
		t.Fatal("the recovery of one source cleared the failure of another")
	}
	metrics.SetBusinessDegraded("sp05/tenant-a/cluster-b", false)
	if metrics.BusinessDegraded() {
		t.Fatal("the recovered sources are still degraded")
	}
	// A removed source never keeps a failure alive.
	metrics.SetBusinessDegraded("sp05/tenant-a/cluster-b", true)
	metrics.UnregisterBusinessComponent("sp05/tenant-a/cluster-b")
	if metrics.BusinessDegraded() {
		t.Fatal("an unregistered source kept the runtime degraded")
	}
}

// The key space is bounded: a caller can not grow it without limit.
func TestBusinessHealthKeySpaceIsBounded(t *testing.T) {
	metrics := NewMetrics()
	for index := 0; index < maxBusinessComponents; index++ {
		if !metrics.RegisterBusinessComponent("sp05/tenant/cluster-" + strings.Repeat("x", index%8) + itoa(index)) {
			t.Fatalf("key %d was refused", index)
		}
	}
	if metrics.RegisterBusinessComponent("sp05/tenant/one-too-many") {
		t.Fatal("the bounded key space was exceeded")
	}
}

// The observability export health keeps its own fixed components and its own
// semantics: an unknown export component is still ignored.
func TestExportHealthKeepsItsOwnComponents(t *testing.T) {
	metrics := NewMetrics()
	metrics.SetComponentDegraded("traces", true)
	if !metrics.Degraded() {
		t.Fatal("the trace export failure was dropped")
	}
	metrics.SetComponentDegraded("sp05", true)
	metrics.SetComponentDegraded("traces", false)
	if metrics.Degraded() {
		t.Fatal("the trace export failure was not cleared")
	}
	metrics.SetComponentDegraded("audit_signing", true)
	metrics.SetComponentDegraded("audit_signing", false)
	if metrics.Degraded() {
		t.Fatal("the audit signing failure was not cleared")
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
