package investigation

import "encoding/json"

// Preserve the exact v2 wire fields; non-Incident v3 tasks omit Incident identity.
func (j Job) MarshalJSON() ([]byte, error) {
	type wire Job
	b, err := json.Marshal(wire(j))
	if err != nil || j.Assistant == nil || j.Assistant.TaskKind == "incident_followup" {
		return b, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	delete(fields, "incidentId")
	delete(fields, "triggerRevision")
	return json.Marshal(fields)
}
