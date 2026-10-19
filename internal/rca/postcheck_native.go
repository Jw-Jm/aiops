package rca

import (
	"encoding/json"
	"ops-platform/internal/bundle"
	"ops-platform/internal/evidence"
	"ops-platform/internal/resource"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type NativePostCheck struct {
	Verdict      string
	SpecDigest   string
	NewAnomalies []string
}

// SpecDigest deliberately excludes status and server metadata. The operator must
// choose the desired historical spec; a command exit code supplies no goal.
func SpecDigest(o unstructured.Unstructured) string {
	spec, ok := o.Object["spec"]
	if !ok {
		if o.GetKind() != "EndpointSlice" {
			return ""
		}
		endpoints := []any{}
		for _, raw := range nativeList(o, "endpoints") {
			if m, ok := raw.(map[string]any); ok {
				endpoints = append(endpoints, map[string]any{"addresses": m["addresses"], "targetRef": m["targetRef"]})
			}
		}
		spec = map[string]any{"addressType": o.Object["addressType"], "ports": o.Object["ports"], "endpoints": endpoints}
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return ""
	}
	raw, err = bundle.CanonicalizeJSON(raw)
	if err != nil {
		return ""
	}
	return evidence.Digest(raw)
}

func nativeString(o unstructured.Unstructured, path ...string) string {
	v, _, _ := unstructured.NestedString(o.Object, path...)
	return v
}
func nativeInt(o unstructured.Unstructured, path ...string) int64 {
	v, _, _ := unstructured.NestedInt64(o.Object, path...)
	return v
}
func nativeList(o unstructured.Unstructured, path ...string) []any {
	v, _, _ := unstructured.NestedSlice(o.Object, path...)
	return v
}
func nativeBool(o unstructured.Unstructured, path ...string) (bool, bool) {
	v, ok, _ := unstructured.NestedBool(o.Object, path...)
	return v, ok
}
func OwnedBy(o, owner unstructured.Unstructured) bool {
	for _, r := range o.GetOwnerReferences() {
		if r.Controller != nil && *r.Controller && string(r.UID) == string(owner.GetUID()) && r.Kind == owner.GetKind() && r.APIVersion == owner.GetAPIVersion() && r.Name == owner.GetName() && o.GetNamespace() == owner.GetNamespace() {
			return true
		}
	}
	return false
}
func condition(o unstructured.Unstructured, name string) string {
	for _, raw := range nativeList(o, "status", "conditions") {
		m, ok := raw.(map[string]any)
		if ok && m["type"] == name {
			v, _ := m["status"].(string)
			return v
		}
	}
	return ""
}
func podHealth(o unstructured.Unstructured) string {
	if o.GetDeletionTimestamp() != nil {
		return "not_resolved"
	}
	phase := nativeString(o, "status", "phase")
	if phase == "Pending" || phase == "Failed" {
		return "not_resolved"
	}
	if phase != "Running" {
		return "inconclusive"
	}
	if condition(o, "Ready") == "False" || condition(o, "Ready") == "Unknown" {
		return "not_resolved"
	}
	if condition(o, "Ready") != "True" {
		return "inconclusive"
	}
	containers := nativeList(o, "spec", "containers")
	statuses := nativeList(o, "status", "containerStatuses")
	if len(containers) == 0 || len(containers) != len(statuses) {
		return "inconclusive"
	}
	for _, raw := range containers {
		c, ok := raw.(map[string]any)
		if !ok {
			return "inconclusive"
		}
		found := false
		for _, raw := range statuses {
			s, ok := raw.(map[string]any)
			if !ok {
				return "inconclusive"
			}
			if s["name"] != c["name"] {
				continue
			}
			found = true
			state, _ := s["state"].(map[string]any)
			if s["ready"] != true || state["running"] == nil {
				return "not_resolved"
			}
		}
		if !found {
			return "inconclusive"
		}
	}
	for _, raw := range nativeList(o, "spec", "initContainers") {
		c, ok := raw.(map[string]any)
		if !ok {
			return "inconclusive"
		}
		found := false
		for _, raw := range nativeList(o, "status", "initContainerStatuses") {
			status, ok := raw.(map[string]any)
			if !ok {
				return "inconclusive"
			}
			if status["name"] != c["name"] {
				continue
			}
			found = true
			state, _ := status["state"].(map[string]any)
			if c["restartPolicy"] == "Always" {
				if status["ready"] != true || state["running"] == nil {
					return "not_resolved"
				}
			} else {
				terminated, ok := state["terminated"].(map[string]any)
				if !ok || terminated["exitCode"] != int64(0) {
					return "not_resolved"
				}
			}
		}
		if !found {
			return "inconclusive"
		}
	}
	return "resolved"
}

// NativePostCheckResources consumes a complete bounded snapshot collected by the
// trusted Worker, with each object independently archived and authorized.
func NativePostCheckResources(r Recipe, es []evidence.Evidence, completed time.Time) (NativePostCheck, error) {
	out := NativePostCheck{Verdict: "inconclusive", NewAnomalies: []string{}}
	baseline, err := BuiltinVersion(r.Name, r.Version)
	if err != nil || !sameJSON(r, baseline) || r.PostCheck != "native-related-health/v1" {
		return out, ErrRecipe
	}
	if len(es) == 0 || len(es) > 40 {
		return out, nil
	}
	objects := []unstructured.Unstructured{}
	seen := map[string]bool{}
	var tenant, cluster, ns string
	for _, e := range es {
		if e.SourceSystem != "kubernetes" || e.QueryTemplateVersion != "command-kubernetes-post-check/v1" || e.ReplayState != "archived_verified" || !e.TimeReliable || !e.ObservedFrom.After(completed) || e.ObservedTo.Before(e.ObservedFrom) || e.EvaluatedAt.Before(e.ObservedTo) || e.ContentDigest != evidence.Digest(e.Data) {
			return out, nil
		}
		var o unstructured.Unstructured
		if json.Unmarshal(e.Data, &o) != nil {
			return out, nil
		}
		id, err := resource.ParseCanonicalID(e.ResourceCanonicalID)
		group := o.GroupVersionKind().Group
		if group == "" {
			group = "core"
		}
		if err != nil || id.Domain != "k8s" || id.APIGroup != group || id.Kind != o.GetKind() || id.StableID != string(o.GetUID()) || id.Tenant != e.TenantID || id.StableID == "" || seen[e.ResourceCanonicalID] {
			return out, nil
		}
		if len(objects) == 0 {
			tenant, cluster, ns = id.Tenant, id.Scope, o.GetNamespace()
		} else if id.Tenant != tenant || id.Scope != cluster || o.GetNamespace() != "" && o.GetNamespace() != ns {
			return out, nil
		}
		seen[e.ResourceCanonicalID] = true
		objects = append(objects, o)
	}
	root := objects[0]
	out.SpecDigest = SpecDigest(root)
	byKind := func(kind string) []unstructured.Unstructured {
		v := []unstructured.Unstructured{}
		for _, o := range objects {
			if o.GetKind() == kind {
				v = append(v, o)
			}
		}
		return v
	}
	// New warnings mean observed after completion, not proven causation by the command.
	for _, o := range byKind("Event") {
		if nativeString(o, "type") != "Warning" {
			continue
		}
		related := false
		for _, target := range objects {
			if target.GetKind() != "Event" && string(target.GetUID()) == nativeString(o, "involvedObject", "uid") {
				related = true
			}
		}
		if !related {
			return out, nil
		}
		when := nativeString(o, "lastTimestamp")
		if when == "" {
			when = nativeString(o, "eventTime")
		}
		if v := nativeString(o, "series", "lastObservedTime"); v != "" {
			when = v
		}
		observed, err := time.Parse(time.RFC3339Nano, when)
		if err != nil || observed.After(es[0].EvaluatedAt) {
			return out, nil
		}
		if observed.After(completed) {
			reason := nativeString(o, "reason")
			if reason == "" {
				reason = "Warning"
			}
			if !slices.Contains(out.NewAnomalies, reason) {
				out.NewAnomalies = append(out.NewAnomalies, reason)
			}
		}
	}
	verdict := "inconclusive"
	switch r.Name {
	case "pod-health":
		if root.GetKind() != "Pod" {
			return out, nil
		}
		// Recreated Pods are never substituted by name; controller chains must exist.
		for _, ref := range root.GetOwnerReferences() {
			if ref.Controller != nil && *ref.Controller {
				found := false
				for _, o := range objects {
					if OwnedBy(root, o) {
						found = true
						if o.GetKind() == "ReplicaSet" {
							parent := false
							for _, d := range byKind("Deployment") {
								parent = parent || OwnedBy(o, d)
							}
							if !parent {
								return out, nil
							}
						}
					}
				}
				if !found {
					return out, nil
				}
			}
		}
		verdict = podHealth(root)
	case "deployment-health":
		if root.GetKind() != "Deployment" || root.GetAPIVersion() != "apps/v1" {
			return out, nil
		}
		desired, ok, _ := unstructured.NestedInt64(root.Object, "spec", "replicas")
		if !ok {
			desired = 1
		}
		if desired <= 0 || root.GetGeneration() <= 0 || nativeInt(root, "status", "observedGeneration") < root.GetGeneration() {
			return out, nil
		}
		verdict = "resolved"
		for _, field := range []string{"replicas", "updatedReplicas", "readyReplicas", "availableReplicas"} {
			if nativeInt(root, "status", field) != desired {
				verdict = "not_resolved"
			}
		}
		if condition(root, "Available") != "True" || condition(root, "Progressing") == "False" || nativeInt(root, "status", "unavailableReplicas") > 0 {
			verdict = "not_resolved"
		}
		count := int64(0)
		for _, p := range byKind("Pod") {
			if p.GetDeletionTimestamp() != nil {
				continue
			}
			owned := false
			for _, rs := range byKind("ReplicaSet") {
				if OwnedBy(rs, root) && OwnedBy(p, rs) {
					owned = true
				}
			}
			if !owned {
				return out, nil
			}
			count++
			expected := nativeList(root, "spec", "template", "spec", "containers")
			actual := nativeList(p, "spec", "containers")
			if len(expected) == 0 || len(expected) != len(actual) {
				return out, nil
			}
			for _, raw := range expected {
				c, ok := raw.(map[string]any)
				if !ok {
					return out, nil
				}
				match := false
				for _, raw := range actual {
					a, ok := raw.(map[string]any)
					if ok && a["name"] == c["name"] && a["image"] == c["image"] {
						match = true
					}
				}
				if !match {
					verdict = "not_resolved"
				}
			}
			health := podHealth(p)
			if health == "inconclusive" {
				return out, nil
			}
			if health != "resolved" {
				verdict = "not_resolved"
			}
		}
		if count != desired {
			verdict = "not_resolved"
		}
	case "service-endpoints-health":
		var service unstructured.Unstructured
		if root.GetKind() == "Service" {
			service = root
		} else if root.GetKind() == "EndpointSlice" {
			for _, s := range byKind("Service") {
				if OwnedBy(root, s) {
					service = s
				}
			}
		}
		if service.GetUID() == "" || nativeString(service, "spec", "type") == "ExternalName" || len(nativeList(service, "spec", "ports")) == 0 {
			return out, nil
		}
		slicesToCheck := byKind("EndpointSlice")
		if root.GetKind() == "EndpointSlice" {
			slicesToCheck = []unstructured.Unstructured{root}
		}
		verdict = "resolved"
		endpoints := 0
		for _, slice := range slicesToCheck {
			if !OwnedBy(slice, service) || slice.GetLabels()["kubernetes.io/service-name"] != service.GetName() {
				return out, nil
			}
			for _, raw := range nativeList(service, "spec", "ports") {
				port, ok := raw.(map[string]any)
				if !ok {
					return out, nil
				}
				name, _ := port["name"].(string)
				protocol, _ := port["protocol"].(string)
				if protocol == "" {
					protocol = "TCP"
				}
				matched := false
				for _, raw := range nativeList(slice, "ports") {
					ep, ok := raw.(map[string]any)
					if !ok {
						return out, nil
					}
					epName, _ := ep["name"].(string)
					epProtocol, _ := ep["protocol"].(string)
					if epProtocol == "" {
						epProtocol = "TCP"
					}
					number, _ := ep["port"].(int64)
					if epName == name && epProtocol == protocol && number > 0 {
						targetValue, hasTarget := port["targetPort"]
						if !hasTarget {
							targetValue = port["port"]
						}
						if target, ok := targetValue.(int64); ok && target != number {
							continue
						}
						matched = true
					}
				}
				if !matched {
					return out, nil
				}
			}
			for _, raw := range nativeList(slice, "endpoints") {
				m, ok := raw.(map[string]any)
				if !ok {
					return out, nil
				}
				ep := unstructured.Unstructured{Object: m}
				endpoints++
				ready, present := nativeBool(ep, "conditions", "ready")
				terminating, _ := nativeBool(ep, "conditions", "terminating")
				serving, hasServing := nativeBool(ep, "conditions", "serving")
				if !present {
					return out, nil
				}
				if !ready || terminating || hasServing && !serving {
					verdict = "not_resolved"
				}
				if nativeString(ep, "targetRef", "kind") != "Pod" || nativeString(ep, "targetRef", "namespace") != ns {
					return out, nil
				}
				found := false
				for _, pod := range byKind("Pod") {
					if string(pod.GetUID()) == nativeString(ep, "targetRef", "uid") && pod.GetName() == nativeString(ep, "targetRef", "name") {
						addresses := nativeList(ep, "addresses")
						if len(addresses) == 0 {
							return out, nil
						}
						ips := []string{nativeString(pod, "status", "podIP")}
						for _, raw := range nativeList(pod, "status", "podIPs") {
							m, ok := raw.(map[string]any)
							if ok {
								if ip, ok := m["ip"].(string); ok {
									ips = append(ips, ip)
								}
							}
						}
						for _, raw := range addresses {
							address, ok := raw.(string)
							if !ok || address == "" || !slices.Contains(ips, address) {
								return out, nil
							}
						}
						for _, raw := range nativeList(service, "spec", "ports") {
							sp, _ := raw.(map[string]any)
							target, named := sp["targetPort"].(string)
							if !named {
								continue
							}
							name, _ := sp["name"].(string)
							protocol, _ := sp["protocol"].(string)
							if protocol == "" {
								protocol = "TCP"
							}
							matched := false
							for _, raw := range nativeList(slice, "ports") {
								port, _ := raw.(map[string]any)
								epName, _ := port["name"].(string)
								if epName != name {
									continue
								}
								for _, raw := range nativeList(pod, "spec", "containers") {
									container, _ := raw.(map[string]any)
									for _, raw := range nativeList(unstructured.Unstructured{Object: container}, "ports") {
										cp, _ := raw.(map[string]any)
										cpProtocol, _ := cp["protocol"].(string)
										if cpProtocol == "" {
											cpProtocol = "TCP"
										}
										if cp["name"] == target && cp["containerPort"] == port["port"] && cpProtocol == protocol {
											matched = true
										}
									}
								}
							}
							if !matched {
								return out, nil
							}
						}
						selector, _, _ := unstructured.NestedStringMap(service.Object, "spec", "selector")
						for key, value := range selector {
							if pod.GetLabels()[key] != value {
								return out, nil
							}
						}
						found = true
						health := podHealth(pod)
						if health == "inconclusive" {
							return out, nil
						}
						if health != "resolved" {
							verdict = "not_resolved"
						}
					}
				}
				if !found {
					return out, nil
				}
			}
		}
		if endpoints == 0 {
			verdict = "not_resolved"
		}
	case "pvc-csi-failure":
		if root.GetKind() != "PersistentVolumeClaim" {
			return out, nil
		}
		phase := nativeString(root, "status", "phase")
		if phase == "Pending" || phase == "Lost" {
			verdict = "not_resolved"
			break
		}
		if phase != "Bound" {
			return out, nil
		}
		var pv unstructured.Unstructured
		for _, o := range byKind("PersistentVolume") {
			if o.GetName() == nativeString(root, "spec", "volumeName") {
				pv = o
			}
		}
		if pv.GetUID() == "" || nativeString(pv, "spec", "claimRef", "uid") != string(root.GetUID()) || nativeString(pv, "spec", "claimRef", "name") != root.GetName() || nativeString(pv, "spec", "claimRef", "namespace") != ns || nativeString(pv, "status", "phase") != "Bound" {
			return out, nil
		}
		driver := nativeString(pv, "spec", "csi", "driver")
		if driver == "" {
			return out, nil
		}
		attachRequired := true
		driverFound := false
		for _, o := range byKind("CSIDriver") {
			if o.GetName() == driver {
				driverFound = true
				if value, ok := nativeBool(o, "spec", "attachRequired"); ok {
					attachRequired = value
				}
			}
		}
		if !driverFound {
			return out, nil
		}
		consumers := 0
		verdict = "resolved"
		for _, pod := range byKind("Pod") {
			uses := false
			for _, raw := range nativeList(pod, "spec", "volumes") {
				v, ok := raw.(map[string]any)
				if !ok {
					return out, nil
				}
				uses = uses || nativeString(unstructured.Unstructured{Object: v}, "persistentVolumeClaim", "claimName") == root.GetName()
			}
			if !uses {
				continue
			}
			mounted := false
			for _, raw := range nativeList(pod, "spec", "volumes") {
				v, _ := raw.(map[string]any)
				if nativeString(unstructured.Unstructured{Object: v}, "persistentVolumeClaim", "claimName") != root.GetName() {
					continue
				}
				for _, raw := range nativeList(pod, "spec", "containers") {
					c, _ := raw.(map[string]any)
					for _, field := range []string{"volumeMounts", "volumeDevices"} {
						for _, raw := range nativeList(unstructured.Unstructured{Object: c}, field) {
							m, _ := raw.(map[string]any)
							if m["name"] == v["name"] {
								mounted = true
							}
						}
					}
				}
			}
			if !mounted {
				return out, nil
			}
			consumers++
			health := podHealth(pod)
			if health == "inconclusive" {
				return out, nil
			}
			if health != "resolved" {
				verdict = "not_resolved"
			}
			if attachRequired {
				attached := false
				for _, va := range byKind("VolumeAttachment") {
					if nativeString(va, "spec", "source", "persistentVolumeName") == pv.GetName() && nativeString(va, "spec", "nodeName") == nativeString(pod, "spec", "nodeName") && nativeString(va, "spec", "attacher") == driver {
						value, ok := nativeBool(va, "status", "attached")
						attached = attached || ok && value && va.Object["status"].(map[string]any)["attachError"] == nil && va.Object["status"].(map[string]any)["detachError"] == nil
					}
				}
				if !attached {
					verdict = "not_resolved"
				}
			}
		}
		if consumers == 0 {
			return out, nil
		}
	}
	if len(out.NewAnomalies) > 0 && verdict != "inconclusive" {
		verdict = "not_resolved"
	}
	out.Verdict = verdict
	return out, nil
}
