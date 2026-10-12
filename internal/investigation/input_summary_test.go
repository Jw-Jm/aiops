package investigation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSafeInputSummary(t *testing.T) {
	raw := json.RawMessage(`{"canonicalId":"resource-public-id","depth":0,"limit":50,"query":"PRIVATE RAW QUERY","password":"SECRET","timeRange":{"start":"2026-10-12T00:00:00Z","end":"2026-10-12T01:00:00Z","token":"SECRET"},"unknown":{"token":"SECRET"}}`)
	out := SafeInputSummary("query_logs", raw)
	if strings.Contains(string(out), "SECRET") || strings.Contains(string(out), "PRIVATE") {
		t.Fatalf("unsafe summary: %s", out)
	}
	var fields map[string]any
	if err := json.Unmarshal(out, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["canonicalId"] != "resource-public-id" || fields["depth"] != float64(0) || fields["rawQueryOmitted"] != true || fields["timeRangeStart"] != "2026-10-12T00:00:00Z" {
		t.Fatalf("missing safe fields: %s", out)
	}
	if string(SafeInputSummary("unknown", raw)) != "{}" {
		t.Fatal("unknown inputs exposed")
	}
	if strings.Contains(string(SafeInputSummary("model", raw)), "PRIVATE") {
		t.Fatal("model prompt exposed")
	}
	if string(SafeInputSummary("query_logs", json.RawMessage(strings.Repeat("x", 65537)))) != "{}" {
		t.Fatal("unbounded summary")
	}
}
