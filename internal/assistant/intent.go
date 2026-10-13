package assistant

import (
	"ops-platform/internal/analytics"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ParseSymptom extracts only explicit resource tokens. Free symptoms never
// establish identity; exact authorized resolution and human confirmation follow.
type SymptomIntent struct {
	TaskKind             string           `json:"taskKind"`
	Selector             ResourceSelector `json:"selector"`
	Status               string           `json:"status"`
	MissingFields        []string         `json:"missingFields"`
	RequiresConfirmation bool             `json:"requiresConfirmation"`
}

var explicitResource = regexp.MustCompile(`(?i)(?:^|[\s，。；：:])(?:pod|deployment|statefulset|daemonset|service|node|persistentvolumeclaim|pvc|ingress|job|cronjob)\s*[:：/]?\s*[\x{0022}'\x{0060}]?([a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?)`)
var kindToken = regexp.MustCompile(`(?i)^(pod|deployment|statefulset|daemonset|service|node|persistentvolumeclaim|pvc|ingress|job|cronjob)`)
var namespaceToken = regexp.MustCompile(`(?i)(?:namespace|ns|命名空间)\s*[:：=]?\s*[\x{0022}'\x{0060}]?([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)`)

func ParseSymptom(text string) (SymptomIntent, error) {
	out := SymptomIntent{TaskKind: "resource_troubleshooting", Status: "clarification", MissingFields: []string{}, RequiresConfirmation: true}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 4000 {
		return out, analytics.ErrInvalid
	}
	matches := explicitResource.FindAllStringSubmatch(text, -1)
	if len(matches) == 1 {
		out.Selector.Name = matches[0][1]
		matched := strings.TrimLeft(matches[0][0], " \t\n，。；：:")
		token := kindToken.FindString(matched)
		for _, kind := range []string{"Pod", "Deployment", "StatefulSet", "DaemonSet", "Service", "Node", "PersistentVolumeClaim", "Ingress", "Job", "CronJob"} {
			if strings.EqualFold(token, kind) {
				out.Selector.Kind = kind
			}
		}
		if strings.EqualFold(token, "pvc") {
			out.Selector.Kind = "PersistentVolumeClaim"
		}
	}
	ns := namespaceToken.FindAllStringSubmatch(text, -1)
	if len(ns) == 1 {
		out.Selector.Namespace = ns[0][1]
	}
	if len(matches) > 1 {
		out.MissingFields = append(out.MissingFields, "single_target")
	}
	if out.Selector.Kind == "" {
		out.MissingFields = append(out.MissingFields, "kind")
	}
	if out.Selector.Name == "" {
		out.MissingFields = append(out.MissingFields, "name")
	}
	if len(ns) > 1 {
		out.MissingFields = append(out.MissingFields, "namespace")
	}
	if len(out.MissingFields) == 0 {
		out.Status = "resolution_required"
	}
	return out, nil
}
