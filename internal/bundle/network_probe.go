package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"ops-platform/internal/profile"
)

// NetworkPolicy creation is asynchronous. Prove enforcement before starting
// dependencies, using the already authenticated/imported PostgreSQL image.
// The public addresses are denial probes, never download or runtime endpoints.
func verifyBootstrapEgress(ctx context.Context, installationNamespace string, p profile.ResolvedProfile, images []ImageArtifact, run CommandRunner) (result error) {
	image := ""
	for _, item := range images {
		if item.Name == "postgresql" {
			image = item.Reference
		}
	}
	if image == "" {
		return errors.New("verified PostgreSQL probe image missing")
	}
	raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", "kube-system", "get", "service", "kube-dns", "-o", "json")
	if err != nil {
		return err
	}
	var service struct {
		Spec struct {
			ClusterIP string `json:"clusterIP"`
		} `json:"spec"`
	}
	if json.Unmarshal(raw, &service) != nil || net.ParseIP(service.Spec.ClusterIP) == nil {
		return errors.New("internal DNS Service IP unavailable")
	}
	name := fmt.Sprintf("ops-offline-policy-%x", time.Now().UnixNano())
	overrides, _ := json.Marshal(map[string]any{"spec": map[string]any{
		"automountServiceAccountToken": false,
		"securityContext":              map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "runAsGroup": 65532, "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
		"containers":                   []any{map[string]any{"name": name, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}}}},
	}})
	script := `set -eu
for attempt in $(seq 1 10); do
  if timeout 4 /bin/bash -c 'exec 3<>/dev/tcp/$1/53' _ "$OPS_DNS_IP" >/dev/null 2>&1; then
    allowed=0
    for address in 1.1.1.1 8.8.8.8 2606:4700:4700::1111; do
      if timeout 3 /bin/bash -c 'exec 3<>/dev/tcp/$1/443' _ "$address" >/dev/null 2>&1; then allowed=1; fi
    done
    if [ "$allowed" = 0 ]; then echo INTERNAL_DNS_REACHABLE_PUBLIC_EGRESS_DENIED; exit 0; fi
  fi
  sleep 2
done
echo NETWORK_POLICY_NOT_ENFORCED
exit 1`
	raw, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", installationNamespace, "run", name,
		"--image="+image, "--image-pull-policy=Never", "--restart=Never", "--labels=ops.platform.io/release=ops-platform,ops.platform.io/component=api",
		"--env=OPS_DNS_IP="+service.Spec.ClusterIP, "--override-type=strategic", "--overrides="+string(overrides), "-o", "json", "--command", "--", "/bin/bash", "-ec", script)
	if err != nil {
		return err
	}
	var created struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	if json.Unmarshal(raw, &created) != nil || created.Metadata.UID == "" {
		return errors.New("created enforcement probe identity unavailable; preserve it for inspection")
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		raw, err := run(cleanupCtx, "kubectl", "--context", p.Kubernetes.Context, "-n", installationNamespace, "get", "pod", name, "-o", "json")
		var live struct {
			Metadata struct {
				UID string `json:"uid"`
			} `json:"metadata"`
		}
		if err != nil || json.Unmarshal(raw, &live) != nil || live.Metadata.UID != created.Metadata.UID {
			result = errors.Join(result, errors.New("probe cleanup ownership cannot be verified"))
			return
		}
		_, err = run(cleanupCtx, "kubectl", "--context", p.Kubernetes.Context, "-n", installationNamespace, "delete", "pod", name, "--wait=true", "--timeout=20s")
		result = errors.Join(result, err)
	}()
	if _, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", installationNamespace, "wait", "--for=jsonpath={.status.phase}=Succeeded", "pod/"+name, "--timeout=120s"); err != nil {
		return err
	}
	raw, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", installationNamespace, "logs", "pod/"+name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(raw)) != "INTERNAL_DNS_REACHABLE_PUBLIC_EGRESS_DENIED" {
		return errors.New("network policy probe lacks both positive internal and denied public controls")
	}
	return nil
}
