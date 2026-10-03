// Selected MIT Community IncidentStatus.get_active/get_closed value model.
// Source: keephq/keep 118b2dc0c7a45f8a22b6317b983cdba6f5b54b5e,
// keep/api/models/db/incident.py. No SQLModel, DB, provider or ee code is copied.
package keepmodel

func Active(status string) bool { return status == "firing" || status == "acknowledged" }
func Closed(status string) bool {
	return status == "resolved" || status == "merged" || status == "deleted"
}
