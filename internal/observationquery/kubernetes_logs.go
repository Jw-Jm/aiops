package observationquery

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
)

const NativeLogVersion = "kubernetes-pod-logs/v1"

// KubernetesLogs reads the current container, without claiming a retained log archive.
type KubernetesLogs struct {
	Client   *kubernetes.Client
	SourceID uuid.UUID
	Revision int64
}
type nativePod struct {
	Metadata struct{ Name, Namespace, UID string }
	Spec     struct{ Containers []struct{ Name string } }
	Status   struct {
		Phase             string
		ContainerStatuses []struct {
			Name, ContainerID string
			RestartCount      int
			State             struct {
				Running *struct{ StartedAt time.Time }
			}
		}
	}
}

func (n KubernetesLogs) inspect(ctx context.Context, scope graph.Scope, target Resource) (nativePod, error) {
	var pod nativePod
	if target.Name == "" || target.NamespaceScope.Namespace == nil || target.NamespaceScope.NamespaceUID == nil || !scope.Allows(target.CanonicalID, *target.NamespaceScope.Namespace) {
		return pod, ErrDenied
	}
	response, err := n.Client.Do(ctx, "GET", nativePodPath(target), nil)
	if err != nil {
		return pod, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 403 || response.StatusCode == 404 {
		return pod, ErrDenied
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(raw) > 256<<10 || response.StatusCode != 200 || json.Unmarshal(raw, &pod) != nil {
		return pod, ErrUnavailable
	}
	if pod.Metadata.UID != target.UID || pod.Metadata.Namespace != *target.NamespaceScope.Namespace || pod.Metadata.Name != target.Name {
		return pod, ErrDenied
	}
	if pod.Status.Phase != "Running" || len(pod.Spec.Containers) != 1 || len(pod.Status.ContainerStatuses) != 1 {
		return pod, ErrUnavailable
	}
	status := pod.Status.ContainerStatuses[0]
	if status.Name != pod.Spec.Containers[0].Name || status.ContainerID == "" || status.State.Running == nil || status.State.Running.StartedAt.IsZero() {
		return pod, ErrUnavailable
	}
	return pod, nil
}
func nativePodPath(target Resource) string {
	return "/api/v1/namespaces/" + url.PathEscape(*target.NamespaceScope.Namespace) + "/pods/" + url.PathEscape(target.Name)
}
func (n KubernetesLogs) source(pod nativePod, target Resource) Source {
	return Source{RegistrationID: n.SourceID, RegistrationRevision: n.Revision, CollectorVersion: NativeLogVersion, QualificationDigest: graph.ScopeDigest(struct {
		Version   string
		SourceID  uuid.UUID
		Revision  int64
		Resource  Resource
		Container any
	}{NativeLogVersion, n.SourceID, n.Revision, target, pod.Status.ContainerStatuses[0]})}
}
func (n KubernetesLogs) Capability(ctx context.Context, scope graph.Scope, target Resource) (Capability, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pod, err := n.inspect(ctx, scope, target)
	if err != nil {
		return Capability{}, err
	}
	// A zero-line read proves the actual pods/log permission without ingesting data.
	response, err := n.Client.Do(ctx, "GET", nativePodPath(target)+"/log?"+url.Values{"container": {pod.Spec.Containers[0].Name}, "tailLines": {"0"}, "timestamps": {"true"}}.Encode(), nil)
	if err != nil {
		return Capability{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return Capability{}, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Second)
	if !now.After(pod.Status.ContainerStatuses[0].State.Running.StartedAt) {
		return Capability{}, ErrUnavailable
	}
	return Capability{Name: "Kubernetes 原生日志（当前容器）", Source: n.source(pod, target), CoverageFrom: pod.Status.ContainerStatuses[0].State.Running.StartedAt, CoverageTo: now, VerifiedUntil: now.Add(30 * time.Second), MaxWindowSeconds: 1800}, nil
}
func (n KubernetesLogs) Read(ctx context.Context, scope graph.Scope, target Resource, request Request) (analytics.QueryResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pod, err := n.inspect(ctx, scope, target)
	if err != nil {
		return analytics.QueryResult{}, err
	}
	expected := n.source(pod, target)
	expected.CredentialRevision = request.Source.CredentialRevision
	if expected != request.Source {
		return analytics.QueryResult{}, ErrDenied
	}
	params := url.Values{"container": {pod.Spec.Containers[0].Name}, "timestamps": {"true"}, "sinceTime": {request.Window.From.UTC().Format(time.RFC3339Nano)}, "limitBytes": {"65536"}}
	response, err := n.Client.Do(ctx, "GET", nativePodPath(target)+"/log?"+params.Encode(), nil)
	if err != nil {
		return analytics.QueryResult{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return analytics.QueryResult{}, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return analytics.QueryResult{}, ErrUnavailable
	}
	samples, err := parseNativeLogs(raw, request)
	if err != nil {
		return samples, err
	}
	after, err := n.inspect(ctx, scope, target)
	if err != nil || graph.ScopeDigest(after.Status.ContainerStatuses) != graph.ScopeDigest(pod.Status.ContainerStatuses) {
		return analytics.QueryResult{}, ErrDenied
	}
	return samples, nil
}

var nativeLevel = regexp.MustCompile(`(?i)(?:^|[\s"=:])(?:level["=: ]+)?(error|warn(?:ing)?|info|debug)(?:[\s"=:]|$)`)

func parseNativeLogs(raw []byte, request Request) (analytics.QueryResult, error) {
	out := analytics.QueryResult{Samples: []analytics.LogSample{}, Warnings: []string{"native_current_container_logs_rotation_coverage_unknown"}}
	if len(raw) >= 64<<10 {
		out.Truncated = true
		last := strings.LastIndexByte(string(raw), '\n')
		if last < 0 {
			return out, ErrUnavailable
		}
		raw = raw[:last+1]
	}
	if !utf8.Valid(raw) {
		return out, ErrUnavailable
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		at, message, ok := strings.Cut(line, " ")
		if !ok {
			return out, ErrUnavailable
		}
		timestamp, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return out, ErrUnavailable
		}
		if timestamp.Before(request.Window.From) || !timestamp.Before(request.Window.To) {
			continue
		}
		level := "unknown"
		if match := nativeLevel.FindStringSubmatch(message); len(match) > 1 {
			level = strings.ToLower(match[1])
			if level == "warning" {
				level = "warn"
			}
		} else if len(message) > 1 && message[1] >= '0' && message[1] <= '9' {
			switch message[0] {
			case 'I':
				level = "info"
			case 'W':
				level = "warn"
			case 'E', 'F':
				level = "error"
			}
		}
		if len(request.Filters) > 0 && request.Filters[0].Value != level {
			continue
		}
		clean := []rune(evidence.Redact(message))
		if len(clean) > 2048 {
			clean = clean[:2048]
			out.Truncated = true
		}
		out.Samples = append(out.Samples, analytics.LogSample{Timestamp: timestamp.UTC(), Message: string(clean), Level: level})
	}
	slices.SortStableFunc(out.Samples, func(a, b analytics.LogSample) int { return b.Timestamp.Compare(a.Timestamp) })
	if len(out.Samples) > request.Budget.MaxRows {
		out.Samples = out.Samples[:request.Budget.MaxRows]
		out.Truncated = true
	}
	return out, nil
}
