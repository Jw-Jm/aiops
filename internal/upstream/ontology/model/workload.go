package model

type Workload struct {
	ID             CanonicalID
	Name           string
	Namespace      string
	ControllerKind string
	Attributes     map[string]any
}

func NewWorkload(cluster, namespace, controllerKind, name, uid string, attrs map[string]any) Workload {
	return Workload{
		ID:             WorkloadID(cluster, namespace, controllerKind, name, uid),
		Name:           name,
		Namespace:      namespace,
		ControllerKind: controllerKind,
		Attributes:     attrs,
	}
}
