package model

import (
	"fmt"
	"strings"
)

type CanonicalID string

type ResourceRef struct {
	Cluster   string
	Group     string
	Kind      string
	Namespace string
	Name      string
	UID       string
	ParentID  string
}

func NewCanonicalID(ref ResourceRef) CanonicalID {
	parts := []string{
		normalize(ref.Cluster),
		normalize(ref.Group),
		normalize(ref.Kind),
		normalize(ref.Namespace),
		normalize(ref.Name),
		normalize(ref.UID),
		normalize(ref.ParentID),
	}
	return CanonicalID(strings.Join(parts, "/"))
}

func (id CanonicalID) String() string {
	return string(id)
}

func normalize(v string) string {
	if v == "" {
		return "_"
	}
	return strings.ReplaceAll(v, "/", "_")
}

func WorkloadID(cluster, namespace, kind, name, uid string) CanonicalID {
	return NewCanonicalID(ResourceRef{Cluster: cluster, Group: "apps", Kind: "Workload", Namespace: namespace, Name: fmt.Sprintf("%s:%s", kind, name), UID: uid})
}
