// Render the existing pinned development manifest into an independently owned
// remote namespace. Never edits a core release, Bundle or formal document.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"strings"
)

func main() {
	manifest := flag.String("manifest", "deploy/addons/deepflow/install.yaml", "base manifest")
	images := flag.String("images", "", "private task image lock JSON")
	defaults := flag.String("agent-defaults", "scripts/deepflow-dev/agent-defaults.json", "checked-in Agent Group resource defaults")
	secret := flag.String("password-file", "", "private database password file")
	flag.Parse()
	defaultRaw, err := os.ReadFile(*defaults)
	must(err)
	var agentDefaults struct {
		Global struct {
			Limits struct {
				Memory int `json:"max_memory"`
				CPU    int `json:"max_millicpus"`
			} `json:"limits"`
		} `json:"global"`
	}
	must(json.Unmarshal(defaultRaw, &agentDefaults))
	if agentDefaults.Global.Limits.Memory <= 0 || agentDefaults.Global.Limits.CPU <= 0 {
		panic("invalid Agent Group resource defaults")
	}
	raw, err := os.ReadFile(*images)
	must(err)
	locks := map[string]string{}
	must(json.Unmarshal(raw, &locks))
	password, err := os.ReadFile(*secret)
	must(err)
	if len(strings.TrimSpace(string(password))) < 24 {
		panic("random private task password required")
	}
	raw, err = os.ReadFile(*manifest)
	must(err)
	text := strings.ReplaceAll(string(raw), "ops-sp02-deepflow-poc", "ops-assistant-deepflow-20261013")
	text = strings.ReplaceAll(text, "ops-sp02-deepflow", "ops-assistant-deepflow")
	text = strings.ReplaceAll(text, "sp02-task-2-5", "assistant-network-20261013")
	text = strings.ReplaceAll(text, "__DEEPFLOW_POC_DB_PASSWORD__", strings.TrimSpace(string(password)))
	decoder := yaml.NewDecoder(strings.NewReader(text))
	encoder := json.NewEncoder(os.Stdout)
	for {
		var obj map[string]any
		err = decoder.Decode(&obj)
		if err == io.EOF {
			break
		}
		must(err)
		if obj == nil {
			continue
		}
		kind := obj["kind"].(string)
		meta := obj["metadata"].(map[string]any)
		name := meta["name"].(string)
		if kind == "ConfigMap" && strings.HasSuffix(name, "network-gate") {
			continue
		}
		if kind == "DaemonSet" || kind == "Deployment" || kind == "StatefulSet" {
			spec := obj["spec"].(map[string]any)
			template := spec["template"].(map[string]any)
			pod := template["spec"].(map[string]any)
			delete(pod, "affinity")
			delete(pod, "imagePullSecrets")
			delete(pod, "dnsConfig")
			if volumes, ok := pod["volumes"].([]any); ok {
				out := []any{}
				for _, v := range volumes {
					if v.(map[string]any)["name"] != "network-policy-gate" {
						out = append(out, v)
					}
				}
				pod["volumes"] = out
			}
			if init, ok := pod["initContainers"].([]any); ok {
				out := []any{}
				for _, v := range init {
					if v.(map[string]any)["name"] != "network-policy-gate" {
						out = append(out, v)
					}
				}
				pod["initContainers"] = out
			}
			for _, key := range []string{"containers", "initContainers"} {
				for _, v := range asSlice(pod[key]) {
					c := v.(map[string]any)
					image := c["image"].(string)
					component := ""
					for _, part := range []string{"deepflow-server", "deepflow-agent", "clickhouse-server", "mysql"} {
						if strings.Contains(image, "/"+part+":") {
							component = part
							break
						}
					}
					locked := locks[component]
					if !strings.Contains(locked, "@sha256:") {
						panic("missing immutable development image: " + component)
					}
					c["image"] = locked
					c["imagePullPolicy"] = "Never"
					if strings.HasPrefix(locked, "localhost:18501/") {
						c["imagePullPolicy"] = "IfNotPresent"
					}
					if key == "containers" {
						memory := "512Mi"
						cpu := "500m"
						if component == "deepflow-agent" {
							memory = fmt.Sprintf("%dMi", agentDefaults.Global.Limits.Memory)
							cpu = fmt.Sprintf("%dm", agentDefaults.Global.Limits.CPU)
						}
						if component == "deepflow-server" {
							memory = "1536Mi"
							cpu = "1"
						}
						if component == "mysql" {
							memory = "1Gi"
						}
						if component == "clickhouse-server" {
							memory = "2Gi"
							cpu = "1"
						}
						if component == "deepflow-server" {
							c["startupProbe"] = map[string]any{"httpGet": map[string]any{"path": "/v1/health/", "port": "server"}, "periodSeconds": 5, "failureThreshold": 120}
							// Avoid restart loops during bounded development-host I/O stalls.
							// Readiness and live-source qualification still require actual replies.
							c["livenessProbe"] = map[string]any{"httpGet": map[string]any{"path": "/v1/health/", "port": "server"}, "periodSeconds": 10, "timeoutSeconds": 5, "failureThreshold": 12}
						}
						requestMemory := "128Mi"
						if component == "deepflow-agent" || component == "mysql" {
							requestMemory = "256Mi"
						}
						c["resources"] = map[string]any{"requests": map[string]string{"cpu": "100m", "memory": requestMemory}, "limits": map[string]string{"cpu": cpu, "memory": memory}}
					}
				}
			}
			if kind == "StatefulSet" {
				for _, v := range asSlice(spec["volumeClaimTemplates"]) {
					v.(map[string]any)["spec"].(map[string]any)["storageClassName"] = ""
				}
			}
		}
		if kind == "PersistentVolumeClaim" {
			obj["spec"].(map[string]any)["storageClassName"] = ""
		}
		if kind == "NetworkPolicy" {
			spec := obj["spec"].(map[string]any)
			egress := asSlice(spec["egress"])
			egress = append(egress, map[string]any{"to": []any{map[string]any{"ipBlock": map[string]string{"cidr": "192.168.0.63/32"}}, map[string]any{"ipBlock": map[string]string{"cidr": "10.96.0.1/32"}}}, "ports": []any{map[string]any{"protocol": "TCP", "port": 6443}, map[string]any{"protocol": "TCP", "port": 443}}})
			spec["egress"] = egress
		}
		must(encoder.Encode(obj))
	}
	// Development storage is retained and prebound only to this namespace.
	for _, claim := range []string{"ops-assistant-deepflow-mysql-data-pvc", "clickhouse-path-ops-assistant-deepflow-clickhouse-0", "clickhouse-storage-path-ops-assistant-deepflow-clickhouse-0"} {
		must(encoder.Encode(map[string]any{"apiVersion": "v1", "kind": "PersistentVolume", "metadata": map[string]any{"name": claim + "-20261013", "labels": map[string]string{"ops.platform.io/test-release": "assistant-network-20261013"}}, "spec": map[string]any{"capacity": map[string]string{"storage": "2Gi"}, "accessModes": []string{"ReadWriteOnce"}, "persistentVolumeReclaimPolicy": "Retain", "storageClassName": "", "claimRef": map[string]string{"namespace": "ops-assistant-deepflow-20261013", "name": claim}, "hostPath": map[string]string{"path": "/opt/ops-assistant-dev-20261012/deepflow-storage/" + claim, "type": "DirectoryOrCreate"}, "nodeAffinity": map[string]any{"required": map[string]any{"nodeSelectorTerms": []any{map[string]any{"matchExpressions": []any{map[string]any{"key": "kubernetes.io/hostname", "operator": "In", "values": []string{"mssc"}}}}}}}}}))
	}
}
func asSlice(v any) []any {
	if v == nil {
		return nil
	}
	return v.([]any)
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "development manifest error:", err)
		os.Exit(1)
	}
}
