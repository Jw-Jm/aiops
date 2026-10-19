package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"ops-platform/internal/graph"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
)

var errPostCheckSnapshot = errors.New("incomplete native post-check snapshot")

func postCheckPath(kind, namespace, name string) string {
	base, plural := "/api/v1/", map[string]string{"Node": "nodes", "Pod": "pods", "Service": "services", "PersistentVolumeClaim": "persistentvolumeclaims", "PersistentVolume": "persistentvolumes", "Event": "events"}[kind]
	switch kind {
	case "Deployment":
		base, plural = "/apis/apps/v1/", "deployments"
	case "ReplicaSet":
		base, plural = "/apis/apps/v1/", "replicasets"
	case "EndpointSlice":
		base, plural = "/apis/discovery.k8s.io/v1/", "endpointslices"
	case "CSIDriver":
		base, plural = "/apis/storage.k8s.io/v1/", "csidrivers"
	case "VolumeAttachment":
		base, plural = "/apis/storage.k8s.io/v1/", "volumeattachments"
	}
	if plural == "" {
		return ""
	}
	if namespace != "" {
		base += "namespaces/" + url.PathEscape(namespace) + "/"
	}
	base += plural
	if name != "" {
		base += "/" + url.PathEscape(name)
	}
	return base
}
func readPostCheckObject(ctx context.Context, client *kubernetes.Client, path string) (unstructured.Unstructured, error) {
	var out unstructured.Unstructured
	response, err := client.Do(ctx, "GET", path, nil)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return out, errPostCheckSnapshot
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (512<<10)+1))
	if err != nil || len(raw) > 512<<10 {
		return out, errPostCheckSnapshot
	}
	if json.Unmarshal(raw, &out) != nil {
		return out, errPostCheckSnapshot
	}
	return out, nil
}

// nativePostCheckObjects keeps all reads within fixed API paths and the bound
// namespace. Pagination, missing identities and unreadable dependencies fail closed.
func nativePostCheckObjects(root unstructured.Unstructured, tenant, cluster string, scope graph.Scope, read func(string) (unstructured.Unstructured, error)) ([]unstructured.Unstructured, error) {
	objects := []unstructured.Unstructured{}
	seen := map[string]bool{}
	ns := root.GetNamespace()
	add := func(o unstructured.Unstructured) error {
		group := o.GroupVersionKind().Group
		if group == "" {
			group = "core"
		}
		id := resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: cluster, APIGroup: group, Kind: o.GetKind(), StableID: string(o.GetUID())}.String()
		if o.GetUID() == "" || o.GetName() == "" || o.GetResourceVersion() == "" || o.GetNamespace() != "" && o.GetNamespace() != ns || !scope.Allows(id, o.GetNamespace()) {
			return errPostCheckSnapshot
		}
		if seen[id] {
			return nil
		}
		if len(objects) >= 40 {
			return errPostCheckSnapshot
		}
		seen[id] = true
		objects = append(objects, o)
		return nil
	}
	get := func(kind, namespace, name string) (unstructured.Unstructured, error) {
		if name == "" {
			return unstructured.Unstructured{}, errPostCheckSnapshot
		}
		o, err := read(postCheckPath(kind, namespace, name))
		if err != nil {
			return o, err
		}
		if o.GetKind() != kind || o.GetName() != name || o.GetNamespace() != namespace {
			return o, errPostCheckSnapshot
		}
		return o, add(o)
	}
	list := func(kind, namespace, selector string) ([]unstructured.Unstructured, error) {
		path := postCheckPath(kind, namespace, "") + "?limit=41"
		if selector != "" {
			path += "&labelSelector=" + url.QueryEscape(selector)
		}
		o, err := read(path)
		if err != nil {
			return nil, err
		}
		if o.GetKind() != kind+"List" || o.GetContinue() != "" {
			return nil, errPostCheckSnapshot
		}
		items, ok, _ := unstructured.NestedSlice(o.Object, "items")
		if !ok || len(items) > 40 {
			return nil, errPostCheckSnapshot
		}
		out := []unstructured.Unstructured{}
		for _, v := range items {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, errPostCheckSnapshot
			}
			item := unstructured.Unstructured{Object: m}
			if item.GetKind() != kind || item.GetNamespace() != namespace || item.GetUID() == "" {
				return nil, errPostCheckSnapshot
			}
			out = append(out, item)
		}
		return out, nil
	}
	if err := add(root); err != nil {
		return nil, err
	}
	switch root.GetKind() {
	case "Pod":
		for _, owner := range root.GetOwnerReferences() {
			if owner.Controller == nil || !*owner.Controller {
				continue
			}
			if owner.Kind != "ReplicaSet" && owner.Kind != "Deployment" {
				return nil, errPostCheckSnapshot
			}
			controller, err := get(owner.Kind, ns, owner.Name)
			if err != nil || !rca.OwnedBy(root, controller) {
				return nil, errPostCheckSnapshot
			}
			if owner.Kind == "ReplicaSet" {
				found := false
				for _, ref := range controller.GetOwnerReferences() {
					if ref.Controller != nil && *ref.Controller && ref.Kind == "Deployment" {
						deployment, err := get("Deployment", ns, ref.Name)
						if err != nil || !rca.OwnedBy(controller, deployment) {
							return nil, errPostCheckSnapshot
						}
						found = true
					}
				}
				if !found {
					return nil, errPostCheckSnapshot
				}
			}
		}
	case "Deployment":
		replicas, err := list("ReplicaSet", ns, "")
		if err != nil {
			return nil, err
		}
		for _, rs := range replicas {
			if rca.OwnedBy(rs, root) {
				if err := add(rs); err != nil {
					return nil, err
				}
			}
		}
		pods, err := list("Pod", ns, "")
		if err != nil {
			return nil, err
		}
		for _, pod := range pods {
			for _, rs := range replicas {
				if rca.OwnedBy(rs, root) && rca.OwnedBy(pod, rs) {
					if err := add(pod); err != nil {
						return nil, err
					}
					break
				}
			}
		}
	case "Service", "EndpointSlice":
		service := root
		selected := []unstructured.Unstructured{}
		if root.GetKind() == "EndpointSlice" {
			name := root.GetLabels()["kubernetes.io/service-name"]
			var err error
			service, err = get("Service", ns, name)
			if err != nil || !rca.OwnedBy(root, service) {
				return nil, errPostCheckSnapshot
			}
			selected = append(selected, root)
		} else {
			items, err := list("EndpointSlice", ns, "kubernetes.io/service-name="+root.GetName())
			if err != nil {
				return nil, err
			}
			for _, slice := range items {
				if !rca.OwnedBy(slice, service) {
					return nil, errPostCheckSnapshot
				}
				if err := add(slice); err != nil {
					return nil, err
				}
				selected = append(selected, slice)
			}
		}
		for _, slice := range selected {
			endpoints, _, _ := unstructured.NestedSlice(slice.Object, "endpoints")
			for _, raw := range endpoints {
				ep, ok := raw.(map[string]any)
				if !ok {
					return nil, errPostCheckSnapshot
				}
				ref, ok := ep["targetRef"].(map[string]any)
				if !ok || ref["kind"] != "Pod" || ref["namespace"] != ns {
					return nil, errPostCheckSnapshot
				}
				name, _ := ref["name"].(string)
				pod, err := get("Pod", ns, name)
				if err != nil || string(pod.GetUID()) != ref["uid"] {
					return nil, errPostCheckSnapshot
				}
			}
		}
	case "PersistentVolumeClaim":
		phase, _, _ := unstructured.NestedString(root.Object, "status", "phase")
		if phase == "Bound" {
			name, _, _ := unstructured.NestedString(root.Object, "spec", "volumeName")
			pv, err := get("PersistentVolume", "", name)
			if err != nil {
				return nil, err
			}
			driver, _, _ := unstructured.NestedString(pv.Object, "spec", "csi", "driver")
			if _, err := get("CSIDriver", "", driver); err != nil {
				return nil, err
			}
			pods, err := list("Pod", ns, "")
			if err != nil {
				return nil, err
			}
			for _, pod := range pods {
				volumes, _, _ := unstructured.NestedSlice(pod.Object, "spec", "volumes")
				for _, raw := range volumes {
					m, ok := raw.(map[string]any)
					if !ok {
						return nil, errPostCheckSnapshot
					}
					claim, _, _ := unstructured.NestedString(m, "persistentVolumeClaim", "claimName")
					if claim == root.GetName() {
						if err := add(pod); err != nil {
							return nil, err
						}
						break
					}
				}
			}
			// Cluster objects still require the initiating subject's explicit scope.
			attachRequired := true
			for _, o := range objects {
				if o.GetKind() == "CSIDriver" {
					if v, ok, _ := unstructured.NestedBool(o.Object, "spec", "attachRequired"); ok {
						attachRequired = v
					}
				}
			}
			if attachRequired {
				attachments, err := list("VolumeAttachment", "", "")
				if err != nil {
					return nil, err
				}
				for _, va := range attachments {
					volume, _, _ := unstructured.NestedString(va.Object, "spec", "source", "persistentVolumeName")
					if volume == name {
						if err := add(va); err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}
	if root.GetKind() != "Node" {
		events, err := list("Event", ns, "")
		if err != nil {
			return nil, err
		}
		uids := map[string]bool{}
		for _, o := range objects {
			uids[string(o.GetUID())] = true
		}
		for _, ev := range events {
			uid, _, _ := unstructured.NestedString(ev.Object, "involvedObject", "uid")
			typ, _, _ := unstructured.NestedString(ev.Object, "type")
			if uids[uid] && typ == "Warning" {
				if err := add(ev); err != nil {
					return nil, err
				}
			}
		}
	}
	// Reject a target replaced or updated while gathering its dependent resources.
	latest, err := read(postCheckPath(root.GetKind(), ns, root.GetName()))
	if err != nil || latest.GetUID() != root.GetUID() || latest.GetResourceVersion() != root.GetResourceVersion() {
		return nil, errPostCheckSnapshot
	}
	return objects, nil
}

// Keep reviewed health/config fields only. Pod environment, Secret references,
// PVC volume handles and arbitrary annotations never enter D0 post-check archives.
func postCheckProjection(o unstructured.Unstructured) unstructured.Unstructured {
	out := unstructured.Unstructured{Object: map[string]any{"apiVersion": o.GetAPIVersion(), "kind": o.GetKind(), "metadata": map[string]any{"name": o.GetName(), "namespace": o.GetNamespace(), "uid": string(o.GetUID()), "generation": o.GetGeneration()}}}
	out.SetOwnerReferences(o.GetOwnerReferences())
	out.SetLabels(o.GetLabels())
	if _, ok := o.Object["spec"]; ok {
		out.Object["spec"] = map[string]any{}
	}
	if o.GetDeletionTimestamp() != nil {
		out.SetDeletionTimestamp(o.GetDeletionTimestamp())
	}
	if name := o.GetLabels()["kubernetes.io/service-name"]; name != "" {
		out.SetLabels(map[string]string{"kubernetes.io/service-name": name})
	}
	fields := map[string][]string{
		"Node":                  {"spec.unschedulable", "status.conditions"},
		"Pod":                   {"spec.nodeName", "spec.containers", "spec.initContainers", "spec.volumes", "status.phase", "status.podIP", "status.podIPs", "status.conditions", "status.containerStatuses", "status.initContainerStatuses"},
		"Deployment":            {"spec.replicas", "spec.template.spec.containers", "status.observedGeneration", "status.replicas", "status.updatedReplicas", "status.readyReplicas", "status.availableReplicas", "status.unavailableReplicas", "status.conditions"},
		"Service":               {"spec.type", "spec.ports", "spec.selector"},
		"EndpointSlice":         {"addressType", "ports", "endpoints"},
		"PersistentVolumeClaim": {"spec.volumeName", "spec.storageClassName", "spec.accessModes", "spec.resources.requests.storage", "status.phase", "status.conditions"},
		"PersistentVolume":      {"spec.claimRef", "spec.csi.driver", "status.phase"},
		"CSIDriver":             {"spec.attachRequired"},
		"VolumeAttachment":      {"spec.source.persistentVolumeName", "spec.nodeName", "spec.attacher", "status.attached", "status.attachError", "status.detachError"},
		"ReplicaSet":            {}, "Event": {"type", "reason", "lastTimestamp", "eventTime", "series.lastObservedTime", "involvedObject.uid"},
	}
	for _, field := range fields[o.GetKind()] {
		path := strings.Split(field, ".")
		v, ok, _ := unstructured.NestedFieldCopy(o.Object, path...)
		if ok {
			_ = unstructured.SetNestedField(out.Object, v, path...)
		}
	}
	for _, path := range [][]string{{"spec", "containers"}, {"spec", "initContainers"}, {"spec", "template", "spec", "containers"}} {
		items, ok, _ := unstructured.NestedSlice(out.Object, path...)
		if ok {
			clean := []any{}
			for _, raw := range items {
				if m, ok := raw.(map[string]any); ok {
					clean = append(clean, map[string]any{"name": m["name"], "image": m["image"], "ports": m["ports"], "restartPolicy": m["restartPolicy"], "volumeMounts": m["volumeMounts"], "volumeDevices": m["volumeDevices"]})
				}
			}
			_ = unstructured.SetNestedSlice(out.Object, clean, path...)
		}
	}
	volumes, ok, _ := unstructured.NestedSlice(out.Object, "spec", "volumes")
	if ok {
		clean := []any{}
		for _, raw := range volumes {
			if m, ok := raw.(map[string]any); ok {
				if claim, ok := m["persistentVolumeClaim"].(map[string]any); ok {
					clean = append(clean, map[string]any{"name": m["name"], "persistentVolumeClaim": map[string]any{"claimName": claim["claimName"]}})
				}
			}
		}
		_ = unstructured.SetNestedSlice(out.Object, clean, "spec", "volumes")
	}
	if conditions, ok, _ := unstructured.NestedSlice(out.Object, "status", "conditions"); ok {
		clean := []any{}
		for _, raw := range conditions {
			if m, ok := raw.(map[string]any); ok {
				clean = append(clean, map[string]any{"type": m["type"], "status": m["status"]})
			}
		}
		_ = unstructured.SetNestedSlice(out.Object, clean, "status", "conditions")
	}
	for _, field := range []string{"containerStatuses", "initContainerStatuses"} {
		if statuses, ok, _ := unstructured.NestedSlice(out.Object, "status", field); ok {
			clean := []any{}
			for _, raw := range statuses {
				if m, ok := raw.(map[string]any); ok {
					state := map[string]any{}
					if original, ok := m["state"].(map[string]any); ok {
						for _, key := range []string{"running", "waiting", "terminated"} {
							if value, ok := original[key].(map[string]any); ok {
								kept := map[string]any{}
								for _, k := range []string{"reason", "exitCode"} {
									if v, ok := value[k]; ok {
										kept[k] = v
									}
								}
								state[key] = kept
							}
						}
					}
					clean = append(clean, map[string]any{"name": m["name"], "ready": m["ready"], "state": state})
				}
			}
			_ = unstructured.SetNestedSlice(out.Object, clean, "status", field)
		}
	}
	for _, field := range []string{"attachError", "detachError"} {
		if _, ok, _ := unstructured.NestedMap(out.Object, "status", field); ok {
			_ = unstructured.SetNestedMap(out.Object, map[string]any{"present": true}, "status", field)
		}
	}
	return out
}
