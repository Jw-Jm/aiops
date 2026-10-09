package action

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

type KubeCredential struct {
	Endpoint  string `json:"endpoint"`
	CA        []byte `json:"ca"`
	Token     string `json:"token"`
	Namespace string `json:"namespace"`
}
type SSHCredential struct {
	Address     string `json:"address"`
	Port        int    `json:"port"`
	Principal   string `json:"principal"`
	Certificate string `json:"certificate"`
	KnownHosts  string `json:"knownHosts"`
}
type RunnerCredentials struct {
	Kubernetes *KubeCredential `json:"kubernetes,omitempty"`
	SSH        *SSHCredential  `json:"ssh,omitempty"`
}
type CredentialProvider interface {
	Issue(context.Context, Profile, Binding, string) (RunnerCredentials, error)
}
type SSHSigning interface {
	SignSSHCertificate(context.Context, string, string, string, time.Duration) (string, error)
}
type Host struct {
	Target, UID, Address, KnownHosts, UserCAPublicKey, Principal, Role, OnboardingRef string
	Port                                                                              int
}
type ExecutionCredentials struct {
	Kubernetes              *KubernetesREST
	KubernetesCA            []byte
	ServiceAccountNamespace string
	Hosts                   []Host
	SSHCA                   SSHSigning
}

func (c ExecutionCredentials) Issue(ctx context.Context, p Profile, b Binding, publicKey string) (RunnerCredentials, error) {
	switch p.Type {
	case "k8s_namespace", "k8s_cluster":
		if c.Kubernetes == nil || len(c.KubernetesCA) == 0 {
			return RunnerCredentials{}, ErrDenied
		}
		parts := strings.Split(p.CredentialRef, "/")
		if len(parts) != 3 || parts[0] != "serviceaccount:" || !namePattern.MatchString(parts[1]) || !namePattern.MatchString(parts[2]) {
			return RunnerCredentials{}, ErrDenied
		}
		if p.Type == "k8s_namespace" && parts[1] != p.Namespace {
			return RunnerCredentials{}, ErrDenied
		}
		// Kubernetes enforces a 600-second minimum TokenRequest lifetime. Tokens
		// remain short-lived (at most the 900-second Profile cap), with fixed SA RBAC
		// and no persistent token Secret. SSH certificates retain the stricter timeout cap.
		tokenSeconds := max(600, b.Options.TimeoutSeconds)
		var out struct {
			Status struct {
				Token   string    `json:"token"`
				Expires time.Time `json:"expirationTimestamp"`
			} `json:"status"`
		}
		input := map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest", "spec": map[string]any{"expirationSeconds": tokenSeconds}}
		err := c.Kubernetes.coreRequest(ctx, "POST", "/api/v1/namespaces/"+parts[1]+"/serviceaccounts/"+parts[2]+"/token", input, &out)
		if err != nil || out.Status.Token == "" || !out.Status.Expires.After(time.Now()) || out.Status.Expires.After(time.Now().Add(time.Duration(tokenSeconds)*time.Second+30*time.Second)) {
			return RunnerCredentials{}, ErrDenied
		}
		return RunnerCredentials{Kubernetes: &KubeCredential{Endpoint: c.Kubernetes.Endpoint, CA: c.KubernetesCA, Token: out.Status.Token, Namespace: p.Namespace}}, nil
	case "ssh_user", "ssh_root":
		if c.SSHCA == nil {
			return RunnerCredentials{}, ErrDenied
		}
		for _, h := range c.Hosts {
			if h.Target == b.Target && h.UID == b.TargetUID && h.Principal == p.Principal && h.OnboardingRef == p.HostOnboardingRef && h.Role == p.CredentialRef {
				if h.Port < 1 || h.Port > 65535 || h.Address == "" || h.KnownHosts == "" {
					return RunnerCredentials{}, ErrDenied
				}
				cert, err := c.SSHCA.SignSSHCertificate(ctx, h.Role, publicKey, h.Principal, time.Duration(b.Options.TimeoutSeconds)*time.Second)
				if err != nil {
					return RunnerCredentials{}, err
				}
				if VerifySSHCertificate(cert, publicKey, h.UserCAPublicKey, h.Principal, time.Duration(b.Options.TimeoutSeconds)*time.Second) != nil {
					return RunnerCredentials{}, ErrDenied
				}
				return RunnerCredentials{SSH: &SSHCredential{Address: h.Address, Port: h.Port, Principal: h.Principal, Certificate: cert, KnownHosts: h.KnownHosts}}, nil
			}
		}
	}
	return RunnerCredentials{}, ErrDenied
}
func Kubeconfig(c KubeCredential) []byte {
	return []byte(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: target\n  cluster:\n    server: %s\n    certificate-authority-data: %s\nusers:\n- name: execution\n  user:\n    token: %s\ncontexts:\n- name: execution\n  context:\n    cluster: target\n    user: execution\n    namespace: %s\ncurrent-context: execution\n", c.Endpoint, base64.StdEncoding.EncodeToString(c.CA), c.Token, c.Namespace))
}
