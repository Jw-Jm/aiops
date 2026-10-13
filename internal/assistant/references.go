package assistant

import (
	"encoding/json"
	"ops-platform/internal/analytics"
	"ops-platform/internal/investigation"
	"regexp"
	"slices"
)

type ReferenceResolution struct {
	Status      string   `json:"status"`
	CanonicalID string   `json:"canonicalId,omitempty"`
	Candidates  []string `json:"candidates"`
}

var pronoun = regexp.MustCompile(`它|该资源|这个(?:资源|Pod|节点)|上述资源|刚才那个|前面那个|(?i)\b(it|that resource|this pod)\b`)

func ResolveReference(message, explicit string, binding ConversationBinding, history []Message) (*ReferenceResolution, error) {
	if explicit == "" && !pronoun.MatchString(message) {
		return nil, nil
	}
	candidates := []string{}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" {
			var answer investigation.AssistantAnswer
			if json.Unmarshal(history[i].Content, &answer) != nil {
				return nil, analytics.ErrInvalid
			}
			candidates = append(candidates, answer.EntityRefs...)
			if answer.Clarification != nil && slices.Contains(answer.Clarification.MissingFields, "referentCanonicalId") {
				verified := map[string]bool{}
				for k := 0; k < i; k++ {
					if history[k].Role == "assistant" {
						var prior investigation.AssistantAnswer
						if json.Unmarshal(history[k].Content, &prior) == nil {
							for _, id := range prior.EntityRefs {
								verified[id] = true
							}
						}
					}
				}
				if binding.ResourceContext != nil {
					for _, r := range binding.ResourceContext.Resources {
						verified[r.CanonicalID] = true
					}
				}
				for _, id := range answer.Clarification.Candidates {
					if verified[id] {
						candidates = append(candidates, id)
					}
				}
			}
			break
		}
	}
	if len(candidates) == 0 && binding.ResourceContext != nil {
		for _, r := range binding.ResourceContext.Resources {
			candidates = append(candidates, r.CanonicalID)
		}
	}
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)
	out := &ReferenceResolution{Status: "clarification", Candidates: candidates}
	if explicit != "" {
		if !slices.Contains(candidates, explicit) {
			return nil, analytics.ErrDenied
		}
		out.Status, out.CanonicalID = "resolved", explicit
	} else if len(candidates) == 1 {
		out.Status, out.CanonicalID = "resolved", candidates[0]
	}
	if len(out.Candidates) > 10 {
		out.Candidates = out.Candidates[:10]
	}
	return out, nil
}
