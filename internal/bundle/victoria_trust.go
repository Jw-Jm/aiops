package bundle

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"

	"ops-platform/internal/profile"
)

type VictoriaTrustTarget struct {
	Name, ServerSecret, CAKey, CredentialKey string
}

func (b BusinessValues) VictoriaTrustTargets(p profile.ResolvedProfile) ([]VictoriaTrustTarget, error) {
	if b.values == nil {
		return nil, errors.New("validated current business values required")
	}
	targets := map[string]VictoriaTrustTarget{}
	for _, raw := range object(b.values, "sp04")["sources"].([]any) {
		source := raw.(map[string]any)
		name, componentName, service, port := textValue(source, "Name"), "victoriaMetrics", "ops-victoria-metrics", "8428"
		if name == "victorialogs" {
			componentName, service, port = "victoriaLogs", "ops-victoria-logs", "9428"
		}
		component := p.Components[componentName]
		if component.Mode != "bundled" {
			continue
		}
		u, err := url.Parse(component.Endpoint)
		if err != nil || component.Namespace != b.Namespace() || u.Scheme != "https" || u.Hostname() != service+"."+b.Namespace()+".svc.cluster.local" || u.Port() != port || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || textValue(object(source, "Binding"), "Endpoint") != component.Endpoint {
			return nil, errors.New("current bundled Victoria requires exact namespace HTTPS/authenticated source endpoint")
		}
		target := VictoriaTrustTarget{name, service + "-security", path.Base(textValue(source, "CAFile")), path.Base(textValue(source, "CredentialFile"))}
		if prior, ok := targets[name]; ok && prior != target {
			return nil, errors.New("native Victoria backend has conflicting credential projections")
		}
		targets[name] = target
	}
	used := map[string]bool{}
	out := []VictoriaTrustTarget{}
	for _, name := range []string{"victoriametrics", "victorialogs"} {
		if target, ok := targets[name]; ok {
			for _, key := range []string{target.CAKey, target.CredentialKey} {
				if used[key] {
					return nil, errors.New("native Victoria trust/credential projections collide")
				}
				used[key] = true
			}
			out = append(out, target)
		}
	}
	return out, nil
}

func (b BusinessValues) SourceCredentialsSecret() string {
	return textValue(object(b.values, "sp04"), "sourceCredentialsSecret")
}

func preflightVictoriaTrust(ctx context.Context, p profile.ResolvedProfile, b BusinessValues, run CommandRunner) error {
	targets, err := b.VictoriaTrustTargets(p)
	if err != nil {
		return err
	}
	for _, target := range targets {
		for _, key := range []string{"ca.pem", "tls.crt", "tls.key", "username", "password"} {
			raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "secret", target.ServerSecret, "-o", "go-template={{if index .data \""+key+"\"}}present{{else}}absent{{end}}")
			if err != nil || strings.TrimSpace(string(raw)) != "present" {
				return errors.New("current bundled Victoria TLS/authentication Secret incomplete")
			}
		}
		for _, key := range []string{target.CAKey, target.CredentialKey} {
			raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "secret", b.SourceCredentialsSecret(), "-o", "go-template={{if index .data \""+key+"\"}}present{{else}}absent{{end}}")
			if err != nil || strings.TrimSpace(string(raw)) != "present" {
				return errors.New("current bundled Victoria source credential/trust projection incomplete")
			}
		}
	}
	return nil
}
