package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type GVR struct{ Group, Version, Resource string }

func (g GVR) Key() string { return g.Group + "/" + g.Version + "/" + g.Resource }
func (g GVR) Path() string {
	if g.Group == "" {
		return "/api/" + g.Version + "/" + g.Resource
	}
	return "/apis/" + g.Group + "/" + g.Version + "/" + g.Resource
}
func CoreRequiredGVRs() []GVR {
	return []GVR{{"", "v1", "pods"}, {"", "v1", "nodes"}, {"", "v1", "services"}, {"", "v1", "configmaps"}, {"", "v1", "persistentvolumeclaims"}, {"", "v1", "persistentvolumes"}, {"", "v1", "events"}, {"apps", "v1", "deployments"}, {"apps", "v1", "replicasets"}, {"apps", "v1", "statefulsets"}, {"apps", "v1", "daemonsets"}, {"batch", "v1", "jobs"}, {"storage.k8s.io", "v1", "storageclasses"}, {"storage.k8s.io", "v1", "csidrivers"}, {"storage.k8s.io", "v1", "csinodes"}, {"storage.k8s.io", "v1", "volumeattachments"}, {"discovery.k8s.io", "v1", "endpointslices"}}
}

type GVRState struct {
	LastListCompletedAt     time.Time     `json:"lastListCompletedAt"`
	LastWatchEventAt        time.Time     `json:"lastWatchEventAt"`
	LastWatchProgressAt     time.Time     `json:"lastWatchProgressAt"`
	LastConnectivityProbeAt time.Time     `json:"lastConnectivityProbeAt"`
	WatchConnected          bool          `json:"watchConnected"`
	WatchContinuous         bool          `json:"watchContinuous"`
	ProjectionQueueLag      time.Duration `json:"projectionQueueLag"`
	LastError               string        `json:"lastError"`
}
type Snapshot struct {
	ObservationOnly bool
	GVR             GVR
	Objects         []unstructured.Unstructured
	State           GVRState
}
type watchEvent struct {
	Type   string          `json:"type"`
	Object json.RawMessage `json:"object"`
}
type decodedWatch struct {
	receivedAt time.Time
	event      watchEvent
	err        error
}
type Client struct {
	base   string
	http   *http.Client
	budget *RateBudget
}

// RateBudget may be shared by separate tenant credentials for the same cluster.
// It contains no credentials or source data.
type RateBudget struct {
	mu                sync.Mutex
	next              time.Time
	interval          time.Duration
	gate              chan struct{}
	controlWaiting    atomic.Int64
	collectionWaiting atomic.Int64
	controlStreak     int // accessed only while holding gate
}

func NewRateBudget(qps, burst int) (*RateBudget, error) {
	if qps < 1 || qps > 20 || burst < 1 || burst > 50 {
		return nil, errors.New("invalid Kubernetes rate budget")
	}
	budget := &RateBudget{interval: time.Second / time.Duration(qps), gate: make(chan struct{}, 1)}
	budget.gate <- struct{}{}
	return budget, nil
}
func (c *Client) ShareRateBudget(b *RateBudget) error {
	if b == nil || b.interval <= 0 || b.gate == nil {
		return errors.New("invalid Kubernetes rate budget")
	}
	c.budget = b
	return nil
}
func NewClient(base string, client *http.Client, qps, burst int) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || client == nil || qps < 1 || qps > 20 || burst < 1 || burst > 50 {
		return nil, errors.New("invalid Kubernetes client or rate budget")
	}
	budget, _ := NewRateBudget(qps, burst)
	return &Client{base: strings.TrimSuffix(base, "/"), http: client, budget: budget}, nil
}

// All GVR list/watch/probe/Lease requests share this limiter per cluster.
// Burst is conservatively one, below the maximum even after idle periods.
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {

	parts := strings.Split(strings.TrimPrefix(path, "/apis/coordination.k8s.io/v1/namespaces/"), "/")
	control := strings.HasPrefix(path, "/apis/coordination.k8s.io/v1/namespaces/") && len(parts) == 3 && parts[0] != "" && parts[1] == "leases" && parts[2] != "" && (method == "GET" || method == "PUT")
	if err := c.budget.waitClass(ctx, control); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if res != nil && res.StatusCode == http.StatusTooManyRequests {
		delay := time.Second
		if seconds, parseErr := strconv.Atoi(res.Header.Get("Retry-After")); parseErr == nil && seconds > 0 {
			delay = time.Duration(min(seconds, 60)) * time.Second
		}
		c.budget.mu.Lock()
		until := time.Now().Add(delay)
		if until.After(c.budget.next) {
			c.budget.next = until
		}
		c.budget.mu.Unlock()
	}
	return res, err
}

// The queue gate gives concurrent Informer/probe/Lease callers a fair turn.
// Only the request at the head reserves a slot; canceled waiters cannot leave
// phantom future reservations. Network I/O occurs after releasing the gate.
func (b *RateBudget) wait(ctx context.Context) error {
	return b.waitClass(ctx, false)
}

// Lease checks and renewal share the same pacer, with at most two control
// reservations before a waiting collection request. No caller gets extra QPS;
// bulk List/probe queues cannot consume the entire Lease renewal deadline, and
// sustained Graph traffic cannot starve source collection.
func (b *RateBudget) waitClass(ctx context.Context, control bool) error {
	waiters := &b.collectionWaiting
	if control {
		waiters = &b.controlWaiting
	}
	waiters.Add(1)
	defer waiters.Add(-1)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.gate:
		}
		yield := (!control && b.controlWaiting.Load() > 0 && b.controlStreak < 2) || (control && b.collectionWaiting.Load() > 0 && b.controlStreak >= 2)
		if !yield {
			break
		}
		b.gate <- struct{}{}
		if !pause(ctx, time.Millisecond) {
			return ctx.Err()
		}
	}
	defer func() { b.gate <- struct{}{} }()

	// Reserve only a request that is actually going to run. Canceled requests
	// cannot leave phantom future reservations and starve Lease renewal.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		wait := time.Until(b.next)
		if wait <= 0 {
			b.next = time.Now().Add(b.interval)
			if control {
				b.controlStreak++
			} else {
				b.controlStreak = 0
			}
			b.mu.Unlock()
			return nil
		}
		b.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

}
func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func (c *Client) Run(ctx context.Context, gvr GVR, publish func(Snapshot) error) error {
	if publish == nil {
		return errors.New("projection sink required")
	}
	state := GVRState{}
	objects := map[string]unstructured.Unstructured{}
	tombstones := map[string]bool{}
	rv := ""
	needList := true
	var projectionReceivedAt time.Time
	emit := func() error {
		all := make([]unstructured.Unstructured, 0, len(objects))
		for _, o := range objects {
			all = append(all, *o.DeepCopy())
		}
		if !projectionReceivedAt.IsZero() {
			state.ProjectionQueueLag = time.Since(projectionReceivedAt)
		}
		if err := publish(Snapshot{GVR: gvr, Objects: all, State: state}); err != nil {
			return err
		}
		if !projectionReceivedAt.IsZero() {
			state.ProjectionQueueLag = time.Since(projectionReceivedAt)
			// Health publication records the full receive→persist→atomic graph
			// publication latency without persisting/deleting an empty resource set.
			if err := publish(Snapshot{GVR: gvr, State: state, ObservationOnly: true}); err != nil {
				return err
			}
			projectionReceivedAt = time.Time{}
		}
		return nil
	}
	for ctx.Err() == nil {
		if needList {
			state.WatchContinuous = false
			state.WatchConnected = false
			state.LastError = "rebuilding"
			if err := emit(); err != nil {
				return err
			}
			replacement := map[string]unstructured.Unstructured{}
			continuation := ""
			listRV := ""
			listErr := error(nil)
			for {
				query := url.Values{"limit": {"500"}}
				if continuation != "" {
					query.Set("continue", continuation)
				}
				res, err := c.Do(ctx, "GET", gvr.Path()+"?"+query.Encode(), nil)
				if err != nil {
					listErr = err
					break
				}
				if res.StatusCode != 200 {
					res.Body.Close()
					listErr = fmt.Errorf("list_http_%d", res.StatusCode)
					break
				}
				var list struct {
					APIVersion string `json:"apiVersion"`
					Kind       string `json:"kind"`
					Metadata   struct {
						ResourceVersion string `json:"resourceVersion"`
						Continue        string `json:"continue"`
					}
					Items []json.RawMessage `json:"items"`
				}
				err = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&list)
				res.Body.Close()
				if err != nil || list.Metadata.ResourceVersion == "" {
					listErr = errors.New("invalid_list")
					break
				}
				if listRV != "" && listRV != list.Metadata.ResourceVersion {
					listErr = errors.New("list_snapshot_changed")
					break
				}
				listRV = list.Metadata.ResourceVersion
				for _, encoded := range list.Items {
					var fields map[string]any
					if json.Unmarshal(encoded, &fields) != nil {
						listErr = errors.New("invalid_list_object")
						break
					}
					raw := unstructured.Unstructured{Object: fields}
					if raw.GetKind() == "" {
						raw.SetKind(strings.TrimSuffix(list.Kind, "List"))
					}
					if raw.GetAPIVersion() == "" {
						raw.SetAPIVersion(list.APIVersion)
					}
					o, err := Project(raw, time.Now())
					if err != nil {
						listErr = err
						break
					}
					if tombstones[string(o.GetUID())] {
						listErr = errors.New("terminal_uid_in_list")
						break
					}
					replacement[string(o.GetUID())] = o
				}
				if listErr != nil {
					break
				}
				if len(replacement) > 100000 {
					listErr = errors.New("resource_budget_exhausted")
					break
				}
				continuation = list.Metadata.Continue
				if continuation == "" {
					break
				}
				if len(replacement) > 100000 {
					listErr = errors.New("resource_budget_exhausted")
					break
				}
			}
			if listErr != nil {
				state.LastError = "list_unavailable"
				if err := emit(); err != nil {
					return err
				}
				if !pause(ctx, time.Second) {
					break
				}
				continue
			}
			objects = replacement
			rv = listRV
			state.LastListCompletedAt = time.Now()
			state.ProjectionQueueLag = 0
			projectionReceivedAt = time.Time{}
			state.LastConnectivityProbeAt = time.Now()
			state.LastError = ""
			needList = false
		}
		watchContext, cancel := context.WithTimeout(ctx, 35*time.Second)
		query := url.Values{"watch": {"true"}, "resourceVersion": {rv}, "allowWatchBookmarks": {"true"}, "timeoutSeconds": {"30"}, "sendInitialEvents": {"true"}, "resourceVersionMatch": {"NotOlderThan"}}
		res, err := c.Do(watchContext, "GET", gvr.Path()+"?"+query.Encode(), nil)
		if err != nil || res.StatusCode != 200 {
			if res != nil {
				if res.StatusCode == 410 {
					needList = true
				}
				res.Body.Close()
			}
			cancel()
			state.WatchConnected = false
			state.WatchContinuous = false
			state.LastError = "watch_unavailable"
			if err := emit(); err != nil {
				return err
			}
			if !pause(ctx, time.Second) {
				break
			}
			continue
		}
		state.WatchConnected = true
		state.WatchContinuous = false
		state.LastError = "watch_initializing"
		state.LastConnectivityProbeAt = time.Now()
		if err := emit(); err != nil {
			res.Body.Close()
			cancel()
			return err
		}
		initialObjects := map[string]unstructured.Unstructured{}
		initializing := true
		decoded := make(chan decodedWatch, 1)
		go func() {
			decoder := json.NewDecoder(io.LimitReader(res.Body, 64<<20))
			for {
				var event watchEvent
				err := decoder.Decode(&event)
				select {
				case decoded <- decodedWatch{event: event, err: err, receivedAt: time.Now()}:
				case <-watchContext.Done():
					return
				}
				if err != nil {
					return
				}
			}
		}()
		probes := time.NewTicker(10 * time.Second)
	watchLoop:
		for {
			var event watchEvent
			var receivedAt time.Time
			select {
			case <-watchContext.Done():
				break watchLoop
			case <-probes.C:
				probeContext, probeCancel := context.WithTimeout(watchContext, 3*time.Second)
				// This separate List checks authorization/connectivity without
				// treating the resourceVersion as a wall clock or rebuilding.
				probe, probeErr := c.Do(probeContext, "GET", gvr.Path()+"?limit=1", nil)
				if probe != nil {
					if probe.StatusCode != 200 {
						probeErr = errors.New("probe_unavailable")
					}
					probe.Body.Close()
				}
				probeCancel()
				if probeErr != nil {
					state.LastError = "connectivity_unverified"
					break watchLoop
				}
				state.LastConnectivityProbeAt = time.Now()
				if err := emit(); err != nil {
					probes.Stop()
					res.Body.Close()
					cancel()
					return err
				}
				continue
			case d := <-decoded:
				if d.err != nil {
					break watchLoop
				}
				event = d.event
				receivedAt = d.receivedAt
				state.ProjectionQueueLag = time.Since(receivedAt)
				projectionReceivedAt = receivedAt
			}
			now := receivedAt
			if event.Type == "ERROR" {
				var status struct{ Code int }
				json.Unmarshal(event.Object, &status)
				if status.Code == 410 {
					needList = true
				}
				state.LastError = "watch_error"
				break
			}
			var obj unstructured.Unstructured
			if json.Unmarshal(event.Object, &obj) != nil {
				state.LastError = "invalid_event"
				needList = true
				break
			}
			if event.Type == "BOOKMARK" {
				if initializing && obj.GetAnnotations()["k8s.io/initial-events-end"] == "true" {
					objects = initialObjects
					initializing = false
					state.WatchContinuous = true
					state.LastError = ""
				}
				if obj.GetResourceVersion() != "" {
					rv = obj.GetResourceVersion()
					state.LastWatchProgressAt = now
					if err := emit(); err != nil {
						probes.Stop()
						res.Body.Close()
						cancel()
						return err
					}
				}
				continue
			}
			if event.Type != "ADDED" && event.Type != "MODIFIED" && event.Type != "DELETED" {
				state.LastError = "invalid_event_type"
				needList = true
				break
			}
			if obj.GetUID() == "" || obj.GetResourceVersion() == "" {
				needList = true
				state.LastError = "invalid_event_identity"
				break
			}
			uid := string(obj.GetUID())
			if initializing {
				if event.Type != "ADDED" || tombstones[uid] || len(initialObjects) >= 100000 {
					state.LastError = "initial_watch_contract_invalid"
					needList = true
					break watchLoop
				}
				projected, err := Project(obj, now)
				if err != nil {
					state.LastError = "initial_watch_projection_invalid"
					needList = true
					break watchLoop
				}
				if old, ok := objects[uid]; ok && old.GetResourceVersion() == projected.GetResourceVersion() {
					projected = old
				}
				initialObjects[uid] = projected
				continue
			}
			if event.Type != "DELETED" && tombstones[uid] {
				continue
			}
			old, found := objects[uid]
			if event.Type != "DELETED" && found && old.GetResourceVersion() == obj.GetResourceVersion() {
				continue
			}
			if event.Type == "DELETED" {
				delete(objects, uid)
				if len(tombstones) >= 100000 {
					state.LastError = "tombstone_budget_exhausted"
					needList = true
					break watchLoop
				}
				tombstones[uid] = true
			} else {
				p, err := Project(obj, now)
				if err != nil {
					needList = true
					break
				}
				for key, current := range objects {
					if key != uid && current.GetName() == p.GetName() && current.GetNamespace() == p.GetNamespace() {
						delete(objects, key)
					}
				}
				objects[uid] = p
			}
			rv = obj.GetResourceVersion()
			state.LastWatchEventAt = now
			state.LastWatchProgressAt = now
			if err := emit(); err != nil {
				res.Body.Close()
				cancel()
				return err
			}
		}
		probes.Stop()
		res.Body.Close()
		cancel()
		state.WatchConnected = false
		state.WatchContinuous = false
		if err := emit(); err != nil {
			return err
		}
	}
	return ctx.Err()
}
