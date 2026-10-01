package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"ops-platform/internal/profile"
)

// External services remain outside our policy selector. Only their observed
// Service selector and target port are permitted from this release's Pods.
func externalServiceEgress(ctx context.Context, p profile.ResolvedProfile, run CommandRunner) ([]map[string]any, error) {
	rules := []map[string]any{}
	for _, name := range []string{"postgresql", "keycloak", "seaweedfs", "openbao", "victoriaMetrics", "victoriaLogs", "vmalert"} {
		component := p.Components[name]
		if component.Mode != "external" {
			continue
		}
		endpoint, err := url.Parse(component.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("invalid external endpoint for %s", name)
		}
		labels := strings.Split(endpoint.Hostname(), ".")
		if len(labels) < 3 || labels[2] != "svc" || !releaseNamePattern.MatchString(labels[0]) || !releaseNamePattern.MatchString(labels[1]) {
			return nil, fmt.Errorf("CAPABILITY_DISABLED: offline core policy for %s requires an in-cluster Service endpoint", name)
		}
		serviceName, namespace := labels[0], labels[1]
		if component.ObjectUID == "" || component.Namespace != namespace || component.Name != serviceName {
			return nil, fmt.Errorf("external Service identity lock is missing or inconsistent for %s", name)
		}
		portText := endpoint.Port()
		if portText == "" {
			switch endpoint.Scheme {
			case "http":
				portText = "80"
			case "https":
				portText = "443"
			case "postgresql":
				portText = "5432"
			}
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid external Service port for %s", name)
		}
		raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", namespace, "get", "service", serviceName, "-o", "json")
		if err != nil {
			return nil, fmt.Errorf("read external Service for %s: %w", name, err)
		}
		var service struct {
			Metadata struct {
				UID       string `json:"uid"`
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Selector map[string]string `json:"selector"`
				Ports    []struct {
					Port       int             `json:"port"`
					Protocol   string          `json:"protocol"`
					TargetPort json.RawMessage `json:"targetPort"`
				} `json:"ports"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(raw, &service); err != nil {
			return nil, fmt.Errorf("decode external Service for %s: %w", name, err)
		}
		if service.Metadata.UID != component.ObjectUID || service.Metadata.Namespace != namespace || service.Metadata.Name != serviceName {
			return nil, fmt.Errorf("external Service object identity changed for %s", name)
		}
		if namespace == "ops-system" {
			continue
		} // Identity is checked even when the internal policy already permits traffic.
		if len(service.Spec.Selector) == 0 {
			return nil, fmt.Errorf("offline core cannot authorize selectorless external Service for %s", name)
		}
		found := false
		for _, servicePort := range service.Spec.Ports {
			if servicePort.Port != port || (servicePort.Protocol != "" && servicePort.Protocol != "TCP") {
				continue
			}
			var target any
			var number int
			if json.Unmarshal(servicePort.TargetPort, &number) == nil && number > 0 && number <= 65535 {
				target = number
			} else {
				var named string
				if json.Unmarshal(servicePort.TargetPort, &named) != nil || !releaseNamePattern.MatchString(named) {
					return nil, fmt.Errorf("invalid external target port for %s", name)
				}
				target = named
			}
			rules = append(rules, map[string]any{"namespace": namespace, "podLabels": service.Spec.Selector, "port": target})
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("external Service has no matching TCP port for %s", name)
		}
	}
	return rules, nil
}
