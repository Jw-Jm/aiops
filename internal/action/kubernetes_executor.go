package action

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
)

// KubernetesTransport is a dedicated execution control-plane client. The
// existing read-only investigation client is deliberately never reused here.
type KubernetesTransport interface {
	CreateJob(context.Context, string, []byte) (string, error)
	DeleteJob(context.Context, string, string, string) error
	JobState(context.Context, string, string) (string, error)
}
type KubernetesExecutor struct {
	Client                                       KubernetesTransport
	RunnerNamespace, APIEndpoint, TrustConfigMap string
}

func (k KubernetesExecutor) Job(d Dispatch) ([]byte, error) {
	if d.Profile.Validate() != nil || d.Token == "" || d.ExecutionID == uuid.Nil || !namePattern.MatchString(k.RunnerNamespace) || k.APIEndpoint == "" || !namePattern.MatchString(k.TrustConfigMap) {
		return nil, ErrInvalid
	}
	if d.Profile.Type != "k8s_namespace" && d.Profile.Type != "k8s_cluster" && d.Profile.Type != "ssh_user" && d.Profile.Type != "ssh_root" {
		return nil, ErrDenied
	}
	labels := map[string]string{"app.kubernetes.io/name": "ops-command-runner", "ops.platform/execution-id": d.ExecutionID.String(), "ops.platform/network-profile": d.Profile.NetworkPolicyRef}
	// Neither Bash nor execution credentials are ever serialized into this Job.
	job := map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": "command-" + d.ExecutionID.String(), "namespace": k.RunnerNamespace, "labels": labels}, "spec": map[string]any{
		"backoffLimit": 0, "activeDeadlineSeconds": d.Binding.Options.TimeoutSeconds, "ttlSecondsAfterFinished": 3600,
		"template": map[string]any{"metadata": map[string]any{"labels": labels}, "spec": map[string]any{
			"restartPolicy": "Never", "serviceAccountName": "ops-command-runner", "automountServiceAccountToken": false, "enableServiceLinks": false,
			"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "runAsGroup": 65532, "fsGroup": 65532, "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
			"containers": []any{map[string]any{"name": "runner", "image": d.Profile.ToolImageDigest, "imagePullPolicy": "Never",
				"command": []string{"/ops-command-runner"}, "args": []string{"--execution-id", d.ExecutionID.String(), "--tenant-id", d.TenantID.String(), "--claim-token", d.Token, "--endpoint", k.APIEndpoint},
				"securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}},
				"resources":       map[string]any{"requests": map[string]string{"cpu": "100m", "memory": "64Mi"}, "limits": map[string]string{"cpu": "1", "memory": "256Mi"}},
				"volumeMounts":    []any{map[string]any{"name": "runtime", "mountPath": "/run/ops"}, map[string]any{"name": "trust", "mountPath": "/etc/ops/trust", "readOnly": true}, map[string]any{"name": "identity", "mountPath": "/var/run/ops-identity", "readOnly": true}}}},
			"volumes": []any{map[string]any{"name": "runtime", "emptyDir": map[string]any{"medium": "Memory", "sizeLimit": "32Mi"}}, map[string]any{"name": "trust", "configMap": map[string]any{"name": k.TrustConfigMap}}, map[string]any{"name": "identity", "projected": map[string]any{"sources": []any{map[string]any{"serviceAccountToken": map[string]any{"path": "token", "audience": "openbao", "expirationSeconds": 600}}}}}},
		}}}}
	return json.Marshal(job)
}
func (k KubernetesExecutor) Dispatch(ctx context.Context, d Dispatch) (string, error) {
	if k.Client == nil {
		return "", ErrDenied
	}
	job, err := k.Job(d)
	if err != nil {
		return "", err
	}
	ref, err := k.Client.CreateJob(ctx, k.RunnerNamespace, job)
	if err != nil {
		return "", errors.New("runner submission uncertain")
	}
	return ref, nil
}
