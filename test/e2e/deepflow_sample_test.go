package e2e

import "testing"

func TestDeepFlowQuerySampleRequiresObservedEndpointsAndCounters(t *testing.T) {
	columns := []string{"ip_0", "ip_1", "server_port", "flow_id", "retrans_tx", "retrans_rx"}
	valid := []any{"192.168.1.2", "192.168.1.3", float64(30130), float64(42), float64(0), float64(1)}
	if _, ok := validateDeepFlowQueryRows(columns, [][]any{valid}); !ok {
		t.Fatal("valid observed sample rejected")
	}
	for _, test := range []struct {
		name  string
		index int
		value any
	}{{"missing source", 0, nil}, {"invalid destination", 1, "unknown"}, {"wrong port", 2, float64(80)}, {"missing flow", 3, nil}, {"missing counter", 4, nil}, {"negative counter", 5, float64(-1)}} {
		t.Run(test.name, func(t *testing.T) {
			row := append([]any(nil), valid...)
			row[test.index] = test.value
			if _, ok := validateDeepFlowQueryRows(columns, [][]any{row}); ok {
				t.Fatal("incomplete query sample accepted")
			}
		})
	}
}

func TestDeepFlowNetworkGateRequiresProgrammedReject(t *testing.T) {
	const ip = "192.168.1.2"
	const rules = `-A KUBE-ROUTER-FORWARD -s 192.168.1.2/32 -j KUBE-POD-FW-TEST
-A KUBE-POD-FW-TEST -s 192.168.1.2/32 -j KUBE-NWPLCY-TEST
-A KUBE-POD-FW-TEST -m mark ! --mark 0x10000/0x10000 -j REJECT --reject-with icmp-port-unreachable
`
	if !deepFlowPolicyRejectsUnmarkedEgress(rules, ip) {
		t.Fatal("complete deny rules rejected")
	}
	for _, input := range []struct{ name, rules, ip string }{{"not programmed", "", ip}, {"public IP", rules, "1.1.1.1"}, {"wrong Pod", rules, "192.168.1.3"}, {"default allow", rules + "-A KUBE-POD-FW-TEST -s 192.168.1.2/32 -j KUBE-NWPLCY-DEFAULT\n", ip}} {
		t.Run(input.name, func(t *testing.T) {
			if deepFlowPolicyRejectsUnmarkedEgress(input.rules, input.ip) {
				t.Fatal("gate opened without applicable programmed deny")
			}
		})
	}
}
