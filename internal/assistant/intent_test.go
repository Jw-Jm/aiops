package assistant

import "testing"

func TestSymptomRequiresExplicitAuthorizedIdentity(t *testing.T) {
	for _, test := range []struct{ text, kind, name, ns, status string }{{"请排查 Pod checkout-api 在 namespace payments 一直重启", "Pod", "checkout-api", "payments", "resolution_required"}, {"Node worker-a NotReady", "Node", "worker-a", "", "resolution_required"}, {"支付服务超时", "", "", "", "clarification"}, {"Pod a 和 Pod b 都失败", "", "", "", "clarification"}, {"pod a namespace x namespace y", "Pod", "a", "", "clarification"}, {"PVC cache-data 挂载失败", "PersistentVolumeClaim", "cache-data", "", "resolution_required"}} {
		out, err := ParseSymptom(test.text)
		if err != nil || out.Status != test.status || out.Selector.Kind != test.kind || out.Selector.Name != test.name || out.Selector.Namespace != test.ns || !out.RequiresConfirmation {
			t.Fatalf("%s: %+v %v", test.text, out, err)
		}
	}
}
