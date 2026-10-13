package assistant

import (
	"encoding/json"
	"ops-platform/internal/investigation"
)

// Six retained rounds provide structured references; only the most recent four
// rounds retain complete messages. Summaries never use a second model call.
func prepareHistory(messages []Message) ([]Message, error) {
	if len(messages) > 12 {
		return nil, investigation.ErrInvalid
	}
	recent := max(0, len(messages)-8)
	out := make([]Message, 0, 10)
	for i, m := range messages {
		if i < recent {
			if m.Role != "assistant" {
				continue
			}
			var answer investigation.AssistantAnswer
			if json.Unmarshal(m.Content, &answer) != nil {
				return nil, investigation.ErrInvalid
			}
			text := []rune(answer.Summary)
			if len(text) > 800 {
				answer.Summary = string(text[:800]) + "…"
			}
			// Preserve facts, source references, asOf, conflicts and missing evidence.
			m.Content = raw(answer)
		}
		out = append(out, m)
	}
	return out, nil
}
