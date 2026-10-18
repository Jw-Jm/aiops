package deepflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/evidence"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
)

// LiveConfig is trusted source registration configuration, never tool input.
// The resource endpoint is the pinned Server API, not the internal database.
type LiveConfig struct{ ControllerEndpoint, DomainLCUUID, ClusterUID string }
type LiveMapper struct {
	config         LiveConfig
	binding        evidence.Binding
	native         *kubernetes.Client
	client         *http.Client
	authorize      evidence.Authorizer
	mu             sync.Mutex
	endpoints      map[string]Endpoint
	snapshotDigest string
}

var nativeName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

func NewLiveMapper(c LiveConfig, b evidence.Binding, n *kubernetes.Client, client *http.Client, authorize evidence.Authorizer) (*LiveMapper, error) {
	u, err := url.Parse(c.ControllerEndpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.Port() == "8123" || u.Port() == "9000" || strings.Contains(strings.ToLower(u.Host), "clickhouse") || n == nil || client == nil || authorize == nil || c.ClusterUID == "" {
		return nil, evidence.ErrArgument
	}
	if _, err = uuid.Parse(c.DomainLCUUID); err != nil {
		return nil, evidence.ErrArgument
	}
	if !slices.Contains(b.ScopeMapping.Scopes["cluster"], c.ClusterUID) || len(b.ScopeMapping.Scopes["organization"]) != 1 || len(b.ScopeMapping.Scopes["namespace"]) == 0 {
		return nil, evidence.ErrScopeUnverified
	}
	for _, ns := range b.ScopeMapping.Scopes["namespace"] {
		if !nativeName.MatchString(ns) {
			return nil, evidence.ErrArgument
		}
	}
	frozen := b
	raw, _ := json.Marshal(b)
	_ = json.Unmarshal(raw, &frozen)
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &LiveMapper{config: c, binding: frozen, native: n, client: &copyClient, authorize: authorize, endpoints: map[string]Endpoint{}}, nil
}
func (m *LiveMapper) ByCanonical(ctx context.Context, id string) (Endpoint, error) {
	canonical, err := resource.ParseCanonicalID(id)
	if err != nil || canonical.Domain != "k8s" || canonical.Kind != "Pod" || canonical.Tenant != m.binding.Tenant || canonical.Scope != m.config.ClusterUID {
		return Endpoint{}, evidence.ErrScopeUnverified
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.endpoints, m.snapshotDigest, err = m.refresh(ctx)
	if err != nil {
		m.endpoints = map[string]Endpoint{}
		return Endpoint{}, err
	}
	endpoint, ok := m.endpoints[id]
	if !ok {
		return Endpoint{}, evidence.ErrScopeUnverified
	}
	return endpoint, nil
}

// ByCanonicalPair resolves both identities from one verified native/backend
// snapshot. Every invocation refreshes; this does not introduce a TTL cache.
func (m *LiveMapper) ByCanonicalPair(ctx context.Context, left, right string) (Endpoint, Endpoint, error) {
	for _, id := range []string{left, right} {
		c, err := resource.ParseCanonicalID(id)
		if err != nil || c.Domain != "k8s" || c.Kind != "Pod" || c.Tenant != m.binding.Tenant || c.Scope != m.config.ClusterUID {
			return Endpoint{}, Endpoint{}, evidence.ErrScopeUnverified
		}
	}
	// Pair reads use a local verified snapshot, so they cannot overwrite the
	// legacy flow reader's backend map while it is resolving returned rows.
	endpoints, _, err := m.refresh(ctx, left, right)
	if err != nil {
		return Endpoint{}, Endpoint{}, err
	}
	l, lok := endpoints[left]
	r, rok := endpoints[right]
	if !lok || !rok {
		return Endpoint{}, Endpoint{}, evidence.ErrScopeUnverified
	}
	return l, r, nil
}

func (m *LiveMapper) ByBackend(ctx context.Context, id uint64) (Endpoint, error) {
	if m.authorize(ctx, m.binding) != nil {
		return Endpoint{}, evidence.ErrScopeUnverified
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var found Endpoint
	count := 0
	for _, e := range m.endpoints {
		if e.PodID == id {
			found = e
			count++
		}
	}
	if count != 1 {
		return Endpoint{}, evidence.ErrScopeUnverified
	}
	return found, nil
}

type nativePod struct {
	Metadata struct {
		UID, Name, Namespace, ResourceVersion string
		CreationTimestamp                     time.Time
		DeletionTimestamp                     *time.Time
	} `json:"metadata"`
	Status struct {
		ContainerStatuses []struct {
			ContainerID string
			State       struct {
				Running *struct{ StartedAt time.Time }
			}
		}
	} `json:"status"`
}
type nativeSnapshot struct {
	Pods       []nativePod
	Namespaces map[string]string
}

func (m *LiveMapper) nativeSnapshot(ctx context.Context) (nativeSnapshot, error) {
	out := nativeSnapshot{Namespaces: map[string]string{}}
	get := func(path string, v any) error {
		started := time.Now()
		defer func() {
			if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
				slog.Warn("DeepFlow native identity read slow", "path", path, "elapsedMs", elapsed.Milliseconds(), "contextDone", ctx.Err() != nil)
			}
		}()
		response, err := m.native.Do(ctx, "GET", path, nil)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return evidence.ErrScopeUnverified
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
		if err != nil || len(raw) > 4<<20 {
			return evidence.ErrBudget
		}
		return json.Unmarshal(raw, v)
	}
	for _, ns := range append([]string{"kube-system"}, m.binding.ScopeMapping.Scopes["namespace"]...) {
		var namespace struct{ Metadata struct{ UID string } }
		if err := get("/api/v1/namespaces/"+ns, &namespace); err != nil {
			return out, err
		}
		out.Namespaces[ns] = namespace.Metadata.UID
	}
	if out.Namespaces["kube-system"] != m.config.ClusterUID {
		return out, evidence.ErrScopeUnverified
	}
	for _, ns := range m.binding.ScopeMapping.Scopes["namespace"] {
		var list struct {
			Metadata struct{ Continue string }
			Items    []nativePod
		}
		if err := get("/api/v1/namespaces/"+ns+"/pods?limit=1000", &list); err != nil {
			return out, err
		}
		if list.Metadata.Continue != "" || len(list.Items) > 1000 {
			return out, evidence.ErrBudget
		}
		out.Pods = append(out.Pods, list.Items...)
	}
	return out, nil
}

type cloudPod struct {
	LCUUID       string    `json:"lcuuid"`
	Name         string    `json:"name"`
	ContainerIDs string    `json:"container_ids"`
	CreatedAt    time.Time `json:"created_at"`
	Namespace    string    `json:"pod_namespace_lcuuid"`
	Cluster      string    `json:"pod_cluster_lcuuid"`
}

func (m *LiveMapper) refresh(ctx context.Context, requested ...string) (out map[string]Endpoint, digest string, err error) {
	// The fence covers native snapshots before and after the controller round
	// trip; on a loaded single-node host that measured above 2.5s, so the
	// bounded allowance is 6s and every caller still bounds it further.
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	stage := "binding"
	started := time.Now()
	stageStarted := started
	timings := map[string]int64{}
	nextStage := func(next string) {
		timings[stage] = time.Since(stageStarted).Milliseconds()
		stage, stageStarted = next, time.Now()
	}
	defer func() {
		if err != nil {
			timings[stage] = time.Since(stageStarted).Milliseconds()
			slog.Warn("DeepFlow identity refresh rejected", "stage", stage, "elapsedMs", time.Since(started).Milliseconds(), "stageMs", timings, "contextDone", ctx.Err() != nil)
		}
	}()
	if m.authorize(ctx, m.binding) != nil {
		return nil, "", evidence.ErrScopeUnverified
	}
	nextStage("native_before")
	before, err := m.nativeSnapshot(ctx)
	if err != nil {
		return nil, "", err
	}
	nextStage("backend")
	endpoints, err := m.resolveSnapshot(ctx, before, requested...)
	if err != nil {
		return nil, "", err
	}
	nextStage("native_after")
	after, err := m.nativeSnapshot(ctx)
	if err != nil {
		return nil, "", err
	}
	fingerprint := func(s nativeSnapshot) string { raw, _ := json.Marshal(s); return evidence.Digest(raw) }
	if fingerprint(before) != fingerprint(after) {
		return nil, "", evidence.ErrScopeUnverified
	}
	nextStage("binding_after")
	if m.authorize(ctx, m.binding) != nil {
		return nil, "", evidence.ErrScopeUnverified
	}
	return endpoints, fingerprint(after), nil
}

// resolveSnapshot maps only native identities from this call's bounded snapshot.
func (m *LiveMapper) resolveSnapshot(ctx context.Context, before nativeSnapshot, requested ...string) (map[string]Endpoint, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", m.config.ControllerEndpoint+"/v1/info/"+m.config.DomainLCUUID+"/", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Org-Id", m.binding.ScopeMapping.Scopes["organization"][0])
	response, err := m.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 || response.StatusCode != 200 {
		return nil, evidence.ErrScopeUnverified
	}
	var envelope struct {
		Status string `json:"OPT_STATUS"`
		Data   struct {
			Verified    bool
			SyncAt      time.Time
			PodClusters []struct {
				LCUUID string `json:"lcuuid"`
				Name   string `json:"name"`
			}
			PodNamespaces []struct {
				LCUUID  string `json:"lcuuid"`
				Name    string `json:"name"`
				Cluster string `json:"pod_cluster_lcuuid"`
			}
			Pods []cloudPod
		} `json:"DATA"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Status != "SUCCESS" || !envelope.Data.Verified || envelope.Data.SyncAt.IsZero() || time.Since(envelope.Data.SyncAt) > 5*time.Minute || envelope.Data.SyncAt.After(time.Now().Add(time.Minute)) {
		return nil, evidence.ErrScopeUnverified
	}
	org, err := strconv.Atoi(m.binding.ScopeMapping.Scopes["organization"][0])
	if err != nil || org < 1 || org > 65535 {
		return nil, evidence.ErrScopeUnverified
	}
	derived := func(uid string) string {
		if org == 1 {
			return uid
		}
		return uuid.NewSHA1(uuid.Nil, []byte(strconv.Itoa(org)+"_"+uid)).String()
	}
	endpoints := map[string]Endpoint{}
	// Reuse identical dimension lookups only within this verified refresh. There
	// is no cross-query backend ID cache; native identities are checked again below.
	dimensions := map[string]uint64{}
	lookup := func(tag, where string) (uint64, error) {
		key := tag + "\x00" + where
		if id, ok := dimensions[key]; ok {
			return id, nil
		}
		id, err := m.tagID(ctx, tag, where)
		if err == nil {
			dimensions[key] = id
		}
		return id, err
	}
	for _, native := range before.Pods {
		canonical := resource.CanonicalID{Domain: "k8s", Tenant: m.binding.Tenant, Scope: m.config.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: native.Metadata.UID}.String()
		if len(requested) > 0 && !slices.Contains(requested, canonical) {
			continue
		}
		if native.Metadata.UID == "" || native.Metadata.DeletionTimestamp != nil || len(native.Status.ContainerStatuses) == 0 {
			continue
		}
		namespaceUID := before.Namespaces[native.Metadata.Namespace]
		if namespaceUID == "" {
			continue
		}
		validFrom := native.Metadata.CreationTimestamp
		containers := []string{}
		running := true
		for _, status := range native.Status.ContainerStatuses {
			parts := strings.SplitN(status.ContainerID, "://", 2)
			if len(parts) != 2 || len(parts[1]) != 64 || status.State.Running == nil || status.State.Running.StartedAt.IsZero() {
				running = false
				break
			}
			containers = append(containers, parts[1])
			if status.State.Running.StartedAt.After(validFrom) {
				validFrom = status.State.Running.StartedAt
			}
		}
		if !running || validFrom.IsZero() {
			continue
		}
		slices.Sort(containers)
		candidates := []cloudPod{}
		for _, pod := range envelope.Data.Pods {
			backendContainers := strings.Split(pod.ContainerIDs, ", ")
			slices.Sort(backendContainers)
			if pod.Namespace == derived(namespaceUID) && pod.Name == native.Metadata.Name && slices.Equal(containers, backendContainers) && absDuration(pod.CreatedAt.Sub(native.Metadata.CreationTimestamp)) < time.Second {
				candidates = append(candidates, pod)
			}
		}
		if len(candidates) != 1 {
			continue
		}
		pod := candidates[0]
		clusterName := ""
		for _, cluster := range envelope.Data.PodClusters {
			if cluster.LCUUID == pod.Cluster {
				clusterName = cluster.Name
			}
		}
		if clusterName == "" {
			continue
		}
		clusterID, err := lookup("pod_cluster", fmt.Sprintf("pod_cluster='%s'", safeNativeName(clusterName)))
		if err != nil {
			continue
		}
		namespaceID, err := lookup("pod_ns", fmt.Sprintf("pod_ns='%s' AND pod_cluster_id=%d", safeNativeName(native.Metadata.Namespace), clusterID))
		if err != nil {
			continue
		}
		podID, err := lookup("pod", fmt.Sprintf("pod='%s' AND pod_cluster_id=%d AND pod_ns_id=%d", safeNativeName(pod.Name), clusterID, namespaceID))
		if err != nil {
			continue
		}
		endpoints[canonical] = Endpoint{CanonicalID: canonical, Namespace: native.Metadata.Namespace, PodID: podID, ClusterID: clusterID, NamespaceID: namespaceID, IdentityValidFrom: validFrom}
	}
	return endpoints, nil
}

type summaryPairSnapshot struct {
	native      nativeSnapshot
	left, right Endpoint
}

func (m *LiveMapper) beginSummaryPair(ctx context.Context, left, right string) (summaryPairSnapshot, error) {
	// One fence pass covers authorize, two native snapshots' worth of work and
	// the per-container tag resolution; on a loaded single-node host that
	// measured above 2.5s, and the caller's batch budget still bounds the pair.
	ctx, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
	defer cancel()
	if m.authorize(ctx, m.binding) != nil {
		return summaryPairSnapshot{}, evidence.ErrScopeUnverified
	}
	before, err := m.nativeSnapshot(ctx)
	if err != nil {
		return summaryPairSnapshot{}, err
	}
	endpoints, err := m.resolveSnapshot(ctx, before, left, right)
	if err != nil {
		return summaryPairSnapshot{}, err
	}
	l, lok := endpoints[left]
	r, rok := endpoints[right]
	if !lok || !rok {
		return summaryPairSnapshot{}, evidence.ErrScopeUnverified
	}
	return summaryPairSnapshot{before, l, r}, nil
}

// Native snapshots enclose every backend/window read in the batch. No result
// is published until both native lifetimes and fresh backend IDs still match.
func (m *LiveMapper) finishSummaryPair(ctx context.Context, snapshot summaryPairSnapshot) (Endpoint, Endpoint, error) {
	ctx, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
	defer cancel()
	if m.authorize(ctx, m.binding) != nil {
		return Endpoint{}, Endpoint{}, evidence.ErrScopeUnverified
	}
	endpoints, err := m.resolveSnapshot(ctx, snapshot.native, snapshot.left.CanonicalID, snapshot.right.CanonicalID)
	if err != nil {
		return Endpoint{}, Endpoint{}, err
	}
	after, err := m.nativeSnapshot(ctx)
	if err != nil {
		return Endpoint{}, Endpoint{}, err
	}
	beforeRaw, _ := json.Marshal(snapshot.native)
	afterRaw, _ := json.Marshal(after)
	if evidence.Digest(beforeRaw) != evidence.Digest(afterRaw) || m.authorize(ctx, m.binding) != nil {
		return Endpoint{}, Endpoint{}, evidence.ErrScopeUnverified
	}
	l, lok := endpoints[snapshot.left.CanonicalID]
	r, rok := endpoints[snapshot.right.CanonicalID]
	if !lok || !rok {
		return Endpoint{}, Endpoint{}, evidence.ErrScopeUnverified
	}
	return l, r, nil
}

func safeNativeName(s string) string {
	if !nativeName.MatchString(s) {
		return ""
	}
	return s
}
func absDuration(v time.Duration) time.Duration {
	if v < 0 {
		return -v
	}
	return v
}
func (m *LiveMapper) tagID(ctx context.Context, tag, where string) (uint64, error) {
	form := url.Values{"db": {"flow_log"}, "sql": {"SHOW TAG " + tag + " VALUES FROM l4_flow_log WHERE " + where + " LIMIT 2"}}
	request, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(m.binding.Endpoint, "/")+"/v1/query/", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-Org-Id", m.binding.ScopeMapping.Scopes["organization"][0])
	response, err := m.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 || response.StatusCode != 200 {
		return 0, evidence.ErrScopeUnverified
	}
	var out struct {
		Status string `json:"OPT_STATUS"`
		Result struct {
			Columns []string            `json:"columns"`
			Values  [][]json.RawMessage `json:"values"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Status != "SUCCESS" || len(out.Result.Values) != 1 {
		return 0, errors.New("backend identity ambiguous or missing")
	}
	index := slices.Index(out.Result.Columns, "value")
	if index < 0 || len(out.Result.Values[0]) != len(out.Result.Columns) {
		return 0, evidence.ErrScopeUnverified
	}
	var id uint64
	if json.Unmarshal(out.Result.Values[0][index], &id) != nil || id == 0 {
		return 0, evidence.ErrScopeUnverified
	}
	return id, nil
}

// beginQuery uses a request-local mapping. Its native snapshot encloses all
// backend and row reads; the final check validates that same snapshot.
func (m *LiveMapper) beginQuery(ctx context.Context) (map[string]Endpoint, string, error) {
	if err := m.authorize(ctx, m.binding); err != nil {
		return nil, "", err
	}
	before, err := m.nativeSnapshot(ctx)
	if err != nil {
		return nil, "", err
	}
	endpoints, err := m.resolveSnapshot(ctx, before)
	if err != nil {
		return nil, "", err
	}
	raw, _ := json.Marshal(before)
	return endpoints, evidence.Digest(raw), nil
}

func (m *LiveMapper) validateSnapshot(ctx context.Context, digest string) error {
	if m.authorize(ctx, m.binding) != nil {
		return evidence.ErrScopeUnverified
	}
	snapshot, err := m.nativeSnapshot(ctx)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(snapshot)
	if evidence.Digest(raw) != digest {
		return evidence.ErrScopeUnverified
	}
	return nil
}

// ValidateCurrent rechecks native UID/container lifetime once for the whole
// bounded result, rather than re-resolving every row through the backend.
func (m *LiveMapper) ValidateCurrent(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateSnapshot(ctx, m.snapshotDigest); err != nil {
		m.endpoints = map[string]Endpoint{}
		return err
	}
	return nil
}
