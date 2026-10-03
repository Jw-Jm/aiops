package redfish

import (
	"context"
	"encoding/json"
	"ops-platform/internal/resource"
	"time"
)

type Inventory struct {
	Entities        []resource.Entity `json:"entities"`
	Partial         bool              `json:"partial"`
	DegradedSources []string          `json:"degradedSources"`
	Warnings        []string          `json:"warnings"`
	Health          string            `json:"health"`
	DiagnosticFacts []DiagnosticFact  `json:"diagnosticFacts,omitempty"`
}

type DiagnosticFact struct {
	ResourceCanonicalID string          `json:"resourceCanonicalId"`
	RuleID              string          `json:"ruleId"`
	Symptom             string          `json:"symptom"`
	State               string          `json:"state"`
	NativeURI           string          `json:"nativeUri"`
	ObservedAt          time.Time       `json:"observedAt"`
	Data                json.RawMessage `json:"data"`
}

func Health(state, health string) string {
	if state != "Enabled" {
		return "unknown"
	}
	switch health {
	case "OK":
		return "normal"
	case "Warning":
		return "degraded"
	case "Critical":
		return "critical"
	default:
		return "unknown"
	}
}
func Collect(ctx context.Context, c Config) (Inventory, error) {
	out := Inventory{Entities: []resource.Entity{}, DegradedSources: []string{}, Warnings: []string{}, Health: "unknown"}
	if c.Diagnostics {
		c.alarms = map[string]bool{}
	}
	degrade := func(code string) {
		out.Partial = true
		out.DegradedSources = []string{"redfish"}
		out.Warnings = append(out.Warnings, code)
	}
	client, cancel, err := connect(ctx, c)
	defer cancel()
	if err != nil {
		degrade("bmc_unavailable")
		return out, nil
	}
	systems, err := client.Service.Systems()
	if err != nil {
		degrade("inventory_unavailable")
		return out, nil
	}
	if len(systems) == 0 {
		degrade("inventory_missing")
		return out, nil
	}
	if len(systems) > 64 {
		degrade("inventory_budget")
		systems = systems[:64]
	}
	serials := map[string]int{}
	uuids := map[string]int{}
	for _, s := range systems {
		uuids[resource.NormalizeHardwareUUID(s.UUID)]++
		if s.SerialNumber != "" {
			serials[s.SerialNumber]++
		}
	}
	now := c.observationTime()
	diagnosticMetricReads := 0
	resolver := resource.NewResolver()
	for _, s := range systems {
		if normalized := resource.NormalizeHardwareUUID(s.UUID); normalized != "" && uuids[normalized] > 1 {
			degrade("uuid_conflict")
			continue
		}
		warningsBefore := len(out.Warnings)
		resolution, err := resolver.Resolve(ctx, resource.SourceRef{Domain: "hardware", Tenant: c.Tenant, Scope: c.Scope, APIGroup: "redfish", Kind: "PhysicalServer", UUID: s.UUID, Serial: s.SerialNumber, Name: s.Name, SourceRegistrationID: c.SourceID, ObservedAt: now})
		if err != nil || resolution.Status != resource.Matched {
			degrade("identity_unresolved")
			continue
		}
		if serials[s.SerialNumber] > 1 {
			degrade("duplicate_serial")
		}
		health := Health(string(s.Status.State), string(s.Status.Health))
		if health == "unknown" {
			degrade("health_missing")
		}
		if out.Health == "unknown" || health == "critical" || health == "degraded" && out.Health != "critical" {
			out.Health = health
		}
		attrs := map[string]any{"health": health, "manufacturer": s.Manufacturer, "model": s.Model, "uuid": resolution.CanonicalID.StableID, "serial": s.SerialNumber}
		rootIndex := len(out.Entities)
		out.Entities = append(out.Entities, resource.Entity{CanonicalID: resolution.CanonicalID.String(), Kind: "PhysicalServer", Name: s.Name, Labels: map[string]string{}, Attributes: attrs, UpdatedAt: now})
		// Gofish performs typed collection decoding. Components use their immutable
		// parent UUID and Redfish member Id, never a duplicated serial or name join.
		add := func(kind, id, name string, raw any) {
			if len(out.Entities) >= 2000 {
				degrade("component_budget")
				return
			}
			if id == "" {
				degrade("component_identity_missing")
				return
			}
			body, _ := json.Marshal(raw)
			var fields map[string]any
			json.Unmarshal(body, &fields)
			safe := map[string]any{}
			for _, key := range []string{"Manufacturer", "Model", "DeviceLocator", "CapacityMiB", "TotalCores", "TotalThreads", "CapacityBytes", "MediaType", "SpeedMbps", "Status"} {
				if v, ok := fields[key]; ok {
					if key == "Status" {
						if status, ok := v.(map[string]any); ok {
							selected := map[string]any{}
							for _, field := range []string{"State", "Health", "HealthRollup"} {
								if value, ok := status[field].(string); ok {
									selected[field] = value
								}
							}
							safe[key] = selected
						}
					} else {
						safe[key] = v
					}
				}
			}
			component := resolution.CanonicalID
			component.Kind = kind
			component.StableID = resolution.CanonicalID.StableID + "/" + id
			out.Entities = append(out.Entities, resource.Entity{CanonicalID: component.String(), Kind: kind, Name: name, Labels: map[string]string{}, Attributes: safe, UpdatedAt: now})
		}
		if items, err := s.Processors(); err != nil {
			degrade("cpu_partial")
		} else {
			for _, item := range items {
				add("CPU", item.ID, item.Name, item)
			}
		}
		if items, err := s.Memory(); err != nil {
			degrade("dimm_partial")
		} else {
			for _, item := range items {
				add("DIMM", item.ID, item.Name, item)
				if !c.Diagnostics || item.ID == "" {
					continue
				}
				canonical := resolution.CanonicalID
				canonical.Kind, canonical.StableID = "DIMM", resolution.CanonicalID.StableID+"/"+item.ID
				fact := func(rule, symptom, state, uri string, data any) {
					if len(out.DiagnosticFacts) >= 128 {
						degrade("diagnostic_fact_budget")
						return
					}
					raw, _ := json.Marshal(data)
					out.DiagnosticFacts = append(out.DiagnosticFacts, DiagnosticFact{ResourceCanonicalID: canonical.String(), RuleID: rule, Symptom: symptom, State: state, NativeURI: uri, ObservedAt: c.observationTime(), Data: raw})
				}
				if item.Status.State == "Enabled" && (item.Status.Health == "Critical" || item.Status.Health == "OK") {
					state := "firing"
					if item.Status.Health == "OK" {
						state = "resolved"
					}
					fact("redfish/dimm-health/v1", "DIMMHealth", state, item.ODataID, map[string]any{"health": item.Status.Health, "observationClock": "collector", "nativeUri": item.ODataID})
				} else {
					degrade("dimm_diagnostic_health_unknown")
				}
				if diagnosticMetricReads >= 64 {
					degrade("diagnostic_metric_budget")
					continue
				}
				diagnosticMetricReads++
				metrics, metricsErr := item.Metrics()
				if metricsErr != nil || metrics == nil {
					degrade("dimm_metrics_unavailable")
					continue
				}
				alarm, present := c.alarms[metrics.ODataID]
				if !present {
					degrade("dimm_current_alarm_missing")
					continue
				}
				if alarm != metrics.HealthData.AlarmTrips.UncorrectableECCError {
					degrade("dimm_metrics_format_drift")
					continue
				}
				state := "resolved"
				if alarm {
					state = "firing"
				}
				fact("redfish/dimm-uncorrectable-ecc/v1", "UncorrectableECC", state, metrics.ODataID, map[string]any{"uncorrectableECC": alarm, "observationClock": "collector", "nativeUri": metrics.ODataID})
			}
		}
		if items, err := s.EthernetInterfaces(); err != nil {
			degrade("nic_partial")
		} else {
			for _, item := range items {
				add("NIC", item.ID, item.Name, item)
			}
		}
		if storage, err := s.Storage(); err != nil {
			degrade("storage_partial")
		} else {
			if len(storage) == 0 {
				degrade("storage_missing")
			}
			for _, controller := range storage {
				if disks, err := controller.Drives(); err != nil {
					degrade("disk_partial")
				} else {
					for _, disk := range disks {
						add("Disk", disk.ID, disk.Name, disk)
					}
				}
			}
		}
		chassis, err := s.Chassis()
		if err != nil {
			degrade("chassis_partial")
		} else {
			if len(chassis) == 0 {
				degrade("chassis_missing")
			}
			for _, ch := range chassis {
				if power, err := ch.Power(); err != nil {
					degrade("psu_partial")
				} else if power != nil {
					for _, psu := range power.PowerSupplies {
						add("PSU", psu.MemberID, psu.Name, psu)
					}
				}
				if thermal, err := ch.Thermal(); err != nil {
					degrade("fan_partial")
				} else if thermal != nil {
					for _, fan := range thermal.Fans {
						add("Fan", fan.MemberID, fan.Name, fan)
					}
				}
			}
		}
		model, err := metal3Projection(out.Entities[rootIndex], out.Entities[rootIndex+1:])
		if err != nil || model.Partial {
			degrade("hardware_model_partial")
		}
		out.Entities[rootIndex].Attributes["metal3HardwareModel"] = model
		if len(out.Warnings) > warningsBefore && health == "normal" {
			out.Entities[rootIndex].Attributes["health"] = "unknown"
		}
	}
	if out.Partial && out.Health == "normal" {
		out.Health = "unknown"
	}
	counts := map[string]int{}
	for _, e := range out.Entities {
		counts[e.CanonicalID]++
	}
	distinct := []resource.Entity{}
	for _, e := range out.Entities {
		if counts[e.CanonicalID] > 1 {
			degrade("component_identity_conflict")
			continue
		}
		distinct = append(distinct, e)
	}
	out.Entities = distinct
	return out, nil
}
