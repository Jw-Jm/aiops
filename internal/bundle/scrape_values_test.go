package bundle

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPlatformScrapeValuesFreezeResolvedCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name        string
		crds        []string
		raw         string
		want        string
		fail        bool
		unavailable bool
	}{
		{name: "none", want: "none"},
		{name: "victoria-priority", crds: []string{"vmservicescrapes.operator.victoriametrics.com", "servicemonitors.monitoring.coreos.com"}, raw: `{"spec":{"names":{"kind":"VMServiceScrape"},"versions":[{"name":"v1beta1","served":true}]}}`, want: "vmservicescrape"},
		{name: "prometheus", crds: []string{"servicemonitors.monitoring.coreos.com"}, raw: `{"spec":{"names":{"kind":"ServiceMonitor"},"versions":[{"name":"v1","served":true}]}}`, want: "servicemonitor"},
		{name: "not-served", crds: []string{"vmservicescrapes.operator.victoriametrics.com"}, raw: `{"spec":{"names":{"kind":"VMServiceScrape"},"versions":[{"name":"v1beta1","served":false}]}}`, fail: true},
		{name: "wrong-version", crds: []string{"vmservicescrapes.operator.victoriametrics.com"}, raw: `{"spec":{"names":{"kind":"VMServiceScrape"},"versions":[{"name":"v1","served":true}]}}`, fail: true},
		{name: "wrong-kind", crds: []string{"vmservicescrapes.operator.victoriametrics.com"}, raw: `{"spec":{"names":{"kind":"ServiceMonitor"},"versions":[{"name":"v1beta1","served":true}]}}`, fail: true},
		{name: "disappeared", crds: []string{"vmservicescrapes.operator.victoriametrics.com"}, fail: true, unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := importProfile()
			p.Kubernetes.CRDs = tc.crds
			calls := 0
			run := func(_ context.Context, program string, args ...string) ([]byte, error) {
				calls++
				if program != "kubectl" || !strings.Contains(strings.Join(args, " "), "get customresourcedefinition") {
					t.Fatal("unexpected monitoring probe")
				}
				if tc.unavailable {
					return nil, errors.New("not found")
				}
				return []byte(tc.raw), nil
			}
			values, err := platformScrapeValues(context.Background(), p, run)
			if tc.fail {
				if err == nil {
					t.Fatal("monitoring capability drift accepted")
				}
				return
			}
			if err != nil || values["scrapeKind"] != tc.want {
				t.Fatalf("values=%v err=%v", values, err)
			}
			if len(tc.crds) == 0 && calls != 0 {
				t.Fatal("no-CRD profile queried or activated monitoring")
			}
		})
	}
}
