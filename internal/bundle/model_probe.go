package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ops-platform/internal/profile"
)

// verifyCurrentModel proves DNS and the configured model from the target
// investigator network boundary. This is a fixed install check, never a Tool.
func verifyCurrentModel(ctx context.Context, p profile.ResolvedProfile, b BusinessValues, images []ImageArtifact, run CommandRunner) (result error) {
	sp06 := object(b.values, "sp06")
	if textValue(sp06, "modelNetworkMode") == "orbstack-host" && p.Kubernetes.Distribution != "orbstack" {
		return errors.New("native model bridge requires an OrbStack target")
	}
	image := ""
	for _, item := range images {
		if item.Name == "holmesgpt" {
			image = item.Reference
		}
	}
	if image == "" {
		return errors.New("authenticated investigator probe image required")
	}
	name := fmt.Sprintf("ops-model-check-%x", time.Now().UnixNano())
	input, _ := json.Marshal(map[string]any{"endpoint": textValue(object(sp06, "model"), "base_url"), "model": textValue(object(sp06, "model"), "model"), "cidrs": sp06["modelCIDRs"]})
	overrides, _ := json.Marshal(map[string]any{"spec": map[string]any{
		"automountServiceAccountToken": false,
		"securityContext":              map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "runAsGroup": 65532, "fsGroup": 65532, "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
		"containers":                   []any{map[string]any{"name": name, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}}, "volumeMounts": []any{map[string]any{"name": "model-key", "mountPath": "/run/model", "readOnly": true}}, "resources": map[string]any{"requests": map[string]string{"cpu": "10m", "memory": "32Mi"}, "limits": map[string]string{"cpu": "100m", "memory": "128Mi"}}}},
		"volumes":                      []any{map[string]any{"name": "model-key", "secret": map[string]any{"secretName": textValue(sp06, "modelKeySecret"), "defaultMode": 288, "items": []any{map[string]any{"key": "apiKey", "path": "key"}}}}},
	}})
	script := `import os,json,socket,ipaddress,urllib.request,urllib.parse
try:
 c=json.loads(os.environ['OPS_MODEL_CHECK'])
 u=urllib.parse.urlparse(c['endpoint'])
 addresses=sorted({x[4][0] for x in socket.getaddrinfo(u.hostname,u.port or (443 if u.scheme=='https' else 80),type=socket.SOCK_STREAM)})
 networks=[ipaddress.ip_network(x,strict=True) for x in c['cidrs']]
 if not addresses or any(not any(ipaddress.ip_address(a) in n for n in networks) for a in addresses):raise ValueError('scope')
 key=open('/run/model/key').read(65537).strip()
 if not key or len(key)>65536 or '\r' in key or '\n' in key:raise ValueError('credential')
 request=urllib.request.Request(c['endpoint']+'/models',headers={'Authorization':'Bearer '+key})
 with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request,timeout=10) as response:raw=response.read(65537)
 if len(raw)>65536:raise ValueError('bound')
 data=json.loads(raw)
 if c['model'] not in {x.get('id') for x in data.get('data',[]) if isinstance(x,dict)}:raise ValueError('model')
 print(json.dumps({'status':'MODEL_ENDPOINT_SCOPE_AND_AVAILABILITY_VERIFIED','addresses':addresses,'model':c['model']}))
except Exception:
 print('MODEL_ENDPOINT_SCOPE_OR_AVAILABILITY_REJECTED')
 raise SystemExit(1)`
	raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "run", name, "--image="+image, "--image-pull-policy=Never", "--restart=Never", "--labels=ops.platform.io/release=ops-platform,ops.platform.io/component=investigator", "--env=OPS_MODEL_CHECK="+string(input), "--override-type=strategic", "--overrides="+string(overrides), "-o", "json", "--command", "--", "python", "-c", script)
	if err != nil {
		return err
	}
	var created struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	if json.Unmarshal(raw, &created) != nil || created.Metadata.UID == "" {
		return errors.New("model probe UID unavailable; preserve for inspection")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		raw, err := run(cleanup, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "pod", name, "-o", "json")
		var live struct {
			Metadata struct {
				UID string `json:"uid"`
			} `json:"metadata"`
		}
		if err != nil || json.Unmarshal(raw, &live) != nil || live.Metadata.UID != created.Metadata.UID {
			result = errors.Join(result, errors.New("model probe cleanup UID differs"))
			return
		}
		_, err = run(cleanup, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "delete", "pod", name, "--wait=true", "--timeout=20s")
		result = errors.Join(result, err)
	}()
	if _, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "wait", "--for=jsonpath={.status.phase}=Succeeded", "pod/"+name, "--timeout=90s"); err != nil {
		return err
	}
	raw, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "logs", "pod/"+name)
	var proof struct {
		Status    string   `json:"status"`
		Addresses []string `json:"addresses"`
		Model     string   `json:"model"`
	}
	if err != nil || len(raw) > 4096 || json.Unmarshal(raw, &proof) != nil || proof.Status != "MODEL_ENDPOINT_SCOPE_AND_AVAILABILITY_VERIFIED" || len(proof.Addresses) == 0 || proof.Model != textValue(object(sp06, "model"), "model") || strings.Contains(string(raw), "Bearer") {
		return errors.New("native model probe failed")
	}
	return nil
}
