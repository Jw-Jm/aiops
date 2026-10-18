package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/evidence"
)

type assistantProofPod struct {
	Name         string   `json:"name"`
	UID          string   `json:"uid"`
	ContainerIDs []string `json:"containerIds"`
}

type assistantWindowProof struct {
	SchemaVersion string `json:"schemaVersion"`
	Status        string `json:"status"`
	Namespace     string `json:"namespace"`
	Release       string `json:"release"`
	Stable        bool   `json:"stable"`
	TimeReliable  bool   `json:"timeReliable"`
	Polls         int    `json:"polls"`
	SourceErrors  []struct {
		Poll  int    `json:"poll"`
		Type  string `json:"type"`
		Stage string `json:"stage"`
	} `json:"sourceErrors"`
	NativePods []assistantProofPod `json:"nativePods"`
	Golden     []struct {
		From              int64          `json:"fromUnix"`
		To                int64          `json:"toUnix"`
		Codes             map[string]int `json:"codes"`
		Refused           int            `json:"refused"`
		TransportFailures int            `json:"transportFailures"`
		UniqueClientPorts int            `json:"uniqueClientPorts"`
	} `json:"golden"`
	Points       struct{ HTTP, TCP string } `json:"observationPoints"`
	Measurements []struct {
		Responses, Errors   int
		FailureObservations int `json:"failureObservations"`
		TCPRecords          int `json:"tcpRecords"`
	} `json:"measurements"`
}

// This finite oracle qualifies only the owned three-window HTTP/TCP scenario.
func validateAssistantWindowProof(raw, faultRaw, podsRaw []byte, cfg assistantLiveConfig, from, to time.Time, sourceFile string) (assistantWindowProof, error) {
	var w assistantWindowProof
	rejected := errors.New("independent three-window proof identity, coverage or reconciliation rejected")
	if json.Unmarshal(raw, &w) != nil || w.SchemaVersion != "fault-chain-window-proof/v1" || w.Status != "PASS" || !w.Stable || w.TimeReliable || w.SourceErrors == nil || w.Polls < 1 || w.Namespace != cfg.Namespace || w.Release != "fault-chain-20261014" || len(w.Golden) != 3 || len(w.Measurements) != 3 || len(w.NativePods) != 2 || w.Points.HTTP != "s-p" || w.Points.TCP != "s" {
		return w, rejected
	}
	// Earlier failed reads do not invalidate a later complete, stable poll.
	for _, sourceError := range w.SourceErrors {
		if sourceError.Poll < 1 || sourceError.Poll >= w.Polls || !slices.Contains([]string{"RuntimeError", "URLError", "HTTPError", "TimeoutError"}, sourceError.Type) || !slices.Contains([]string{"", "confirm"}, sourceError.Stage) {
			return w, rejected
		}
	}
	for i, g := range w.Golden {
		m := w.Measurements[i]
		errors, refused := []int{10, 50, 0}[i], []int{0, 100, 0}[i]
		if g.To-g.From != 60 || i > 0 && g.From != w.Golden[i-1].To || g.Codes["200"] != 1000-errors || g.Codes["503"] != errors || g.Refused != refused || g.TransportFailures != 0 || g.UniqueClientPorts != 1000+refused || m.Responses != 1000 || m.Errors != errors || m.FailureObservations != refused || m.TCPRecords != 1000+refused {
			return w, rejected
		}
	}
	if time.Unix(w.Golden[0].From, 0).Add(-5*time.Minute).Before(from) || time.Unix(w.Golden[2].To, 0).After(to) {
		return w, rejected
	}
	var fault struct {
		SchemaVersion, Status, Namespace, Release string
		TenantID, ObservationPoint                string
		SourceProofFile, SourceProofDigest        string
		Stable, ConfirmMatches, TimeReliable      bool
		NativePods                                []assistantProofPod
		Window                                    struct {
			From int64 `json:"fromUnix"`
			To   int64 `json:"toUnix"`
		}
		Golden struct{ Responses, Errors, TransportFailures int }
	}
	if json.Unmarshal(faultRaw, &fault) != nil || fault.SchemaVersion != "fault-chain-http-proof/v1" || fault.Status != "PASS" || !fault.Stable || !fault.ConfirmMatches || fault.TimeReliable || fault.TenantID != cfg.TenantID.String() || fault.Namespace != w.Namespace || fault.Release != w.Release || fault.ObservationPoint != w.Points.HTTP || fault.SourceProofFile != filepath.Base(sourceFile) || fault.SourceProofDigest != evidence.Digest(raw) || fault.Window.From != w.Golden[1].From || fault.Window.To != w.Golden[1].To || fault.Golden.Responses != 1000 || fault.Golden.Errors != 50 || fault.Golden.TransportFailures != 0 {
		return w, rejected
	}
	if len(fault.NativePods) != len(w.NativePods) {
		return w, rejected
	}
	for i, pod := range w.NativePods {
		if pod.Name != fault.NativePods[i].Name || pod.UID != fault.NativePods[i].UID || !slices.Equal(pod.ContainerIDs, fault.NativePods[i].ContainerIDs) {
			return w, rejected
		}
	}
	var live struct {
		Items []struct {
			Metadata struct {
				Name, UID, Namespace string
				Labels               map[string]string
			}
			Status struct {
				ContainerStatuses []struct {
					ContainerID string
					State       struct {
						Running *struct{ StartedAt time.Time }
					}
				}
			}
		}
	}
	if json.Unmarshal(podsRaw, &live) != nil {
		return w, rejected
	}
	matched := map[string]bool{}
	for _, p := range live.Items {
		if p.Metadata.Labels["ops.platform.io/test-release"] != w.Release || !slices.Contains([]string{"fault-chain-client", "fault-chain-server"}, p.Metadata.Labels["app"]) {
			continue
		}
		for _, pod := range w.NativePods {
			if p.Metadata.Name != pod.Name {
				continue
			}
			if matched[pod.Name] || p.Metadata.Namespace != w.Namespace || p.Metadata.UID != pod.UID || pod.UID == "" || len(p.Status.ContainerStatuses) == 0 {
				return w, rejected
			}
			containers := []string{}
			for _, c := range p.Status.ContainerStatuses {
				if c.ContainerID == "" || c.State.Running == nil || c.State.Running.StartedAt.IsZero() || c.State.Running.StartedAt.After(time.Unix(w.Golden[0].From, 0)) {
					return w, rejected
				}
				containers = append(containers, c.ContainerID)
			}
			slices.Sort(containers)
			expected := slices.Clone(pod.ContainerIDs)
			slices.Sort(expected)
			if !slices.Equal(containers, expected) {
				return w, rejected
			}
			matched[pod.Name] = true
		}
	}
	if len(matched) != 2 {
		return w, rejected
	}
	return w, nil
}

func TestAssistantWindowProofRejectsUnboundOrIncompleteEvidence(t *testing.T) {
	// Retained real reports also guard compatibility with the probe's JSON format.
	base := "../../docs/evidence/five-source-reverify-20261018/round4"
	raw, err := os.ReadFile(filepath.Join(base, "reports/windows-r4.json"))
	if err != nil {
		t.Fatal(err)
	}
	fault, err := os.ReadFile(filepath.Join(base, "reports/fault-window-r4.json"))
	if err != nil {
		t.Fatal(err)
	}
	var w assistantWindowProof
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	cfg := assistantLiveConfig{TenantID: uuid.MustParse("2984df90-08c9-4a6d-8e32-8a7ec5f41db1"), Namespace: w.Namespace}
	items := []any{}
	for i, p := range w.NativePods {
		items = append(items, map[string]any{"metadata": map[string]any{"name": p.Name, "uid": p.UID, "namespace": w.Namespace, "labels": map[string]string{"app": []string{"fault-chain-client", "fault-chain-server"}[i], "ops.platform.io/test-release": w.Release}}, "status": map[string]any{"containerStatuses": []any{map[string]any{"containerID": p.ContainerIDs[0], "state": map[string]any{"running": map[string]any{"startedAt": time.Unix(w.Golden[0].From-60, 0)}}}}}})
	}
	pods, _ := json.Marshal(map[string]any{"items": items})
	from, to := time.Unix(w.Golden[0].From-340, 0), time.Unix(w.Golden[2].To+60, 0)
	validate := func(raw, fault, pods []byte, cfg assistantLiveConfig, from, to time.Time) error {
		_, err := validateAssistantWindowProof(raw, fault, pods, cfg, from, to, "windows-r4.json")
		return err
	}
	if err := validate(raw, fault, pods, cfg, from, to); err != nil {
		t.Fatal(err)
	}
	mutate := func(raw []byte, change func(map[string]any)) []byte {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		change(doc)
		out, _ := json.Marshal(doc)
		return out
	}
	recovered := mutate(raw, func(d map[string]any) {
		d["polls"] = 2
		d["sourceErrors"] = []any{map[string]any{"poll": 1, "type": "TimeoutError"}}
	})
	recoveredFault := mutate(fault, func(d map[string]any) { d["sourceProofDigest"] = evidence.Digest(recovered) })
	if err := validate(recovered, recoveredFault, pods, cfg, from, to); err != nil {
		t.Fatal("bounded recovery with final stable reconciliation rejected", err)
	}
	for _, test := range []struct {
		name             string
		raw, fault, pods []byte
		cfg              assistantLiveConfig
		from, to         time.Time
	}{
		{"unstable", mutate(raw, func(d map[string]any) { d["stable"] = false }), fault, pods, cfg, from, to},
		{"source-error", mutate(raw, func(d map[string]any) { d["sourceErrors"] = []any{map[string]string{"type": "TimeoutError"}} }), fault, pods, cfg, from, to},
		{"incomplete-count", mutate(raw, func(d map[string]any) { d["measurements"].([]any)[0].(map[string]any)["tcpRecords"] = 999 }), fault, pods, cfg, from, to},
		{"wrong-tenant", raw, fault, pods, assistantLiveConfig{TenantID: uuid.New(), Namespace: cfg.Namespace}, from, to},
		{"wrong-digest", raw, mutate(fault, func(d map[string]any) { d["sourceProofDigest"] = "sha256:other" }), pods, cfg, from, to},
		{"wrong-fault-window", raw, mutate(fault, func(d map[string]any) { d["window"].(map[string]any)["toUnix"] = 1 }), pods, cfg, from, to},
		{"stale-uid", raw, fault, mutate(pods, func(d map[string]any) {
			d["items"].([]any)[0].(map[string]any)["metadata"].(map[string]any)["uid"] = "new-uid"
		}), cfg, from, to},
		{"restarted-container", raw, fault, mutate(pods, func(d map[string]any) {
			d["items"].([]any)[0].(map[string]any)["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]any)["containerID"] = "containerd://new"
		}), cfg, from, to},
		{"short-lookback", raw, fault, pods, cfg, time.Unix(w.Golden[0].From-299, 0), to},
		{"short-recovery", raw, fault, pods, cfg, from, time.Unix(w.Golden[2].To-1, 0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if validate(test.raw, test.fault, test.pods, test.cfg, test.from, test.to) == nil {
				t.Fatal("unqualified proof accepted")
			}
		})
	}
}
