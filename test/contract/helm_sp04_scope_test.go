package contract_test

import (
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type sp04PolicyResource struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string            `yaml:"name"`
		Namespace string            `yaml:"namespace"`
		Labels    map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	RoleRef struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"roleRef"`
	Subjects []struct {
		Kind      string `yaml:"kind"`
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"subjects"`
	Rules []struct {
		APIGroups     []string `yaml:"apiGroups"`
		Resources     []string `yaml:"resources"`
		ResourceNames []string `yaml:"resourceNames"`
		Verbs         []string `yaml:"verbs"`
	} `yaml:"rules"`
}

// The chart must render exactly the runtime collection model: namespaced
// kinds are readable only inside the task namespace, cluster-scoped reads are
// limited to the six kinds without a namespace, and no Secret or write verb
// exists. A global collection ClusterRole would widen the credential beyond the
// declared data scope.
func TestSP04CollectorRBACMatchesRuntimeCollectionScope(t *testing.T) {
	for _, namespaces := range [][]string{{"ops-task"}, {"ops-task", "ops-second"}} {
		t.Run(strings.Join(namespaces, "+"), func(t *testing.T) {
			testSP04CollectorRBAC(t, namespaces)
		})
	}
}

func testSP04CollectorRBAC(t *testing.T, namespaces []string) {
	t.Helper()
	args := []string{
		"template", "ops-platform", "../../deploy/charts/ops-platform", "--namespace", "ops-task",
		"--set", "workloadsEnabled=true",
		"--set", "components.api.image=registry.example.invalid/platform/api@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"--set", "components.worker.image=registry.example.invalid/platform/worker@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"--set", "components.web.image=registry.example.invalid/platform/web@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"--set", "runtime.oidcIssuerURL=https://keycloak.example.invalid", "--set", "runtime.profile=core",
		"--set", "sp04.enabled=true", "--set", "sp04.workerReplicas=2",
		"--set", "sp04.archiveBackendLogicalID=r32-task-archive",
		"--set", "sp04.allowedWorkerCIDRs={10.0.0.1/32}",
		"--set", "sp04.kubernetesAPI.localCollector=true",
		"--set", "sp04.kubernetesAPI.cidrs={10.0.0.2/32}",
		"--set", "sp04.leaseNames={r32-graph-lease}",
		"--set", "sp04.clusters[0].Tenant=01a177f1-372f-75b7-be90-e8121d1445f5",
		"--set", "sp04.clusters[0].ClusterUID=79014400-9453-4cb4-9678-0fae47981d79",
		"--set", "sp04.clusters[0].SourceID=01a17c3c-ef66-70cf-9ff1-cad25a8bee34",
		"--set", "sp04.clusters[0].BackendLogicalID=r32-native-dev-cluster",
		"--set", "sp04.clusters[0].SourceRevision=2",
		"--set", "sp04.clusters[0].Namespaces={" + strings.Join(namespaces, ",") + "}",
	}
	command := exec.Command("helm", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("render sp04 collector scope: %v: %s", err, output)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(output)))
	var policies []sp04PolicyResource
	var projectedTokens []int64
	for {
		var resource struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name      string            `yaml:"name"`
				Namespace string            `yaml:"namespace"`
				Labels    map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
			RoleRef struct {
				Kind string `yaml:"kind"`
				Name string `yaml:"name"`
			} `yaml:"roleRef"`
			Subjects []struct {
				Kind      string `yaml:"kind"`
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"subjects"`
			Rules []struct {
				APIGroups     []string `yaml:"apiGroups"`
				Resources     []string `yaml:"resources"`
				ResourceNames []string `yaml:"resourceNames"`
				Verbs         []string `yaml:"verbs"`
			} `yaml:"rules"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							VolumeMounts []struct {
								Name      string `yaml:"name"`
								MountPath string `yaml:"mountPath"`
								ReadOnly  bool   `yaml:"readOnly"`
							} `yaml:"volumeMounts"`
						} `yaml:"containers"`
						Volumes []struct {
							Name      string `yaml:"name"`
							Projected *struct {
								Sources []struct {
									ServiceAccountToken *struct {
										Path              string `yaml:"path"`
										ExpirationSeconds int64  `yaml:"expirationSeconds"`
									} `yaml:"serviceAccountToken"`
								} `yaml:"sources"`
							} `yaml:"projected"`
						} `yaml:"volumes"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if err := decoder.Decode(&resource); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("decode rendered sp04 resources: %v", err)
		}
		switch resource.Kind {
		case "Role", "ClusterRole", "RoleBinding", "ClusterRoleBinding":
			if strings.Contains(resource.Metadata.Name, "sp04") {
				policy := sp04PolicyResource{Kind: resource.Kind, RoleRef: resource.RoleRef, Rules: resource.Rules}
				policy.Metadata.Name = resource.Metadata.Name
				policy.Metadata.Namespace = resource.Metadata.Namespace
				policy.Subjects = resource.Subjects
				policies = append(policies, policy)
			}
		case "Deployment":
			if resource.Metadata.Labels["ops.platform.io/component"] != "worker" {
				continue
			}
			for _, volume := range resource.Spec.Template.Spec.Volumes {
				if volume.Name != "sp04-kubernetes" || volume.Projected == nil {
					continue
				}
				for _, source := range volume.Projected.Sources {
					if source.ServiceAccountToken != nil {
						projectedTokens = append(projectedTokens, source.ServiceAccountToken.ExpirationSeconds)
					}
				}
			}
		}
	}
	if len(policies) != 4+2*len(namespaces) {
		t.Fatalf("rendered %d sp04 RBAC resources, want collect Role+RoleBinding, cluster-read ClusterRole+ClusterRoleBinding and lease Role+RoleBinding: %+v", len(policies), policies)
	}
	namespacedKinds := map[string]bool{"pods": true, "services": true, "configmaps": true, "persistentvolumeclaims": true, "events": true, "deployments": true, "replicasets": true, "statefulsets": true, "daemonsets": true, "jobs": true, "endpointslices": true}
	clusterScopedKinds := map[string]bool{"nodes": true, "persistentvolumes": true, "storageclasses": true, "csidrivers": true, "csinodes": true, "volumeattachments": true, "namespaces": true}
	for _, policy := range policies {
		switch policy.Kind {
		case "Role", "RoleBinding":
			allowed := policy.Metadata.Namespace == "ops-task"
			if strings.Contains(policy.Metadata.Name, "sp04-collect") {
				allowed = false
				for _, namespace := range namespaces {
					allowed = allowed || policy.Metadata.Namespace == namespace
				}
			}
			if !allowed {
				t.Fatalf("%s %s grants undeclared namespace %q", policy.Kind, policy.Metadata.Name, policy.Metadata.Namespace)
			}
		}
		for _, rule := range policy.Rules {
			for _, verb := range rule.Verbs {
				if verb != "get" && verb != "list" && verb != "watch" && verb != "update" && verb != "patch" {
					t.Fatalf("%s %s grants verb %q", policy.Kind, policy.Metadata.Name, verb)
				}
			}
			for _, resource := range rule.Resources {
				if resource == "namespaces" {
					if policy.Kind != "ClusterRole" || len(rule.Verbs) != 1 || rule.Verbs[0] != "get" || len(rule.ResourceNames) != len(namespaces) {
						t.Fatalf("namespace metadata grants widened: %+v", policy)
					}
					for _, name := range rule.ResourceNames {
						found := false
						for _, expected := range namespaces {
							if name == expected {
								found = true
							}
						}
						if !found {
							t.Fatalf("unregistered namespace metadata: %s", name)
						}
					}
				}
				if strings.Contains(resource, "secrets") {
					t.Fatalf("%s %s grants Secret access", policy.Kind, policy.Metadata.Name)
				}
				if namespacedKinds[resource] && policy.Kind == "ClusterRole" {
					t.Fatalf("ClusterRole %s grants namespaced kind %q at cluster scope", policy.Metadata.Name, resource)
				}
				if clusterScopedKinds[resource] && policy.Kind == "Role" {
					t.Fatalf("Role %s grants cluster-scoped kind %q", policy.Metadata.Name, resource)
				}
			}
		}
		if policy.Kind == "ClusterRole" {
			for _, rule := range policy.Rules {
				for _, resource := range rule.Resources {
					if !clusterScopedKinds[resource] && resource != "nodes" && resource != "pods" {
						t.Fatalf("ClusterRole %s grants unexpected resource %q", policy.Metadata.Name, resource)
					}
				}
			}
		}
	}
	for _, namespace := range namespaces {
		counts := map[string]int{}
		for _, policy := range policies {
			if strings.Contains(policy.Metadata.Name, "sp04-collect") && policy.Metadata.Namespace == namespace {
				counts[policy.Kind]++
				if policy.Kind == "RoleBinding" && (policy.RoleRef.Kind != "Role" || policy.RoleRef.Name != policy.Metadata.Name || len(policy.Subjects) != 1 || policy.Subjects[0].Kind != "ServiceAccount" || policy.Subjects[0].Name != "ops-worker" || policy.Subjects[0].Namespace != "ops-task") {
					t.Fatalf("namespace %s binding targets another identity: %+v", namespace, policy)
				}
			}
		}
		if counts["Role"] != 1 || counts["RoleBinding"] != 1 {
			t.Fatalf("namespace %s needs exactly one Role and binding, got %v", namespace, counts)
		}
	}
	var leaseRole *sp04PolicyResource
	for i := range policies {
		if policies[i].Kind == "Role" && strings.Contains(policies[i].Metadata.Name, "lease") {
			leaseRole = &policies[i]
		}
	}
	if leaseRole == nil || len(leaseRole.Rules) != 1 || len(leaseRole.Rules[0].ResourceNames) != 1 || leaseRole.Rules[0].ResourceNames[0] != "r32-graph-lease" {
		t.Fatalf("lease Role is not restricted to the provisioned lease name: %+v", leaseRole)
	}
	if len(projectedTokens) != 1 || projectedTokens[0] != 600 {
		t.Fatalf("worker cluster token projection = %v, want one 600s kubelet-rotated token", projectedTokens)
	}
}

// A cluster entry without an explicit namespace scope must be refused at
// render time: the collector only lists and watches the declared namespaces.
func TestSP04RenderRejectsClusterWithoutNamespaceScope(t *testing.T) {
	base := []string{
		"template", "ops-platform", "../../deploy/charts/ops-platform", "--namespace", "ops-task",
		"--set", "workloadsEnabled=true",
		"--set", "components.api.image=registry.example.invalid/platform/api@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"--set", "components.worker.image=registry.example.invalid/platform/worker@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"--set", "components.web.image=registry.example.invalid/platform/web@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"--set", "runtime.oidcIssuerURL=https://keycloak.example.invalid", "--set", "runtime.profile=core",
		"--set", "sp04.enabled=true", "--set", "sp04.workerReplicas=2",
		"--set", "sp04.archiveBackendLogicalID=r32-task-archive",
		"--set", "sp04.allowedWorkerCIDRs={10.0.0.1/32}",
		"--set", "sp04.kubernetesAPI.localCollector=true",
		"--set", "sp04.kubernetesAPI.cidrs={10.0.0.2/32}",
		"--set", "sp04.leaseNames={r32-graph-lease}",
		"--set", "sp04.clusters[0].Tenant=01a177f1-372f-75b7-be90-e8121d1445f5",
		"--set", "sp04.clusters[0].ClusterUID=79014400-9453-4cb4-9678-0fae47981d79",
		"--set", "sp04.clusters[0].SourceID=01a17c3c-ef66-70cf-9ff1-cad25a8bee34",
		"--set", "sp04.clusters[0].BackendLogicalID=r32-native-dev-cluster",
		"--set", "sp04.clusters[0].SourceRevision=2",
	}
	command := exec.Command("helm", base...)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("cluster without a namespace scope rendered successfully:\n%s", output)
	} else if !strings.Contains(string(output), "explicit namespace scope") {
		t.Fatalf("unexpected render failure:\n%s", output)
	}
}
