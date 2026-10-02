package redfish

import (
	"encoding/json"
	"ops-platform/internal/inspection/hardware"
	"ops-platform/internal/resource"
	"ops-platform/internal/upstream/metal3"
)

func metal3Projection(root resource.Entity, components []resource.Entity) (hardware.Metal3Inventory, error) {
	text := func(attrs map[string]any, key string) string { v, _ := attrs[key].(string); return v }
	number := func(attrs map[string]any, key string) int64 {
		switch value := attrs[key].(type) {
		case float64:
			return int64(value)
		case int64:
			return value
		case int:
			return int64(value)
		}
		return 0
	}
	model := metal3.HardwareDetails{SystemVendor: metal3.HardwareSystemVendor{Manufacturer: text(root.Attributes, "manufacturer"), ProductName: text(root.Attributes, "model"), SerialNumber: text(root.Attributes, "serial")}, Hostname: root.Name, Storage: []metal3.Storage{}, NIC: []metal3.NIC{}}
	for _, e := range components {
		switch e.Kind {
		case "CPU":
			model.CPU.Count += int(number(e.Attributes, "TotalCores"))
			model.CPU.Model = text(e.Attributes, "Model")
		case "DIMM":
			model.RAMMebibytes += int(number(e.Attributes, "CapacityMiB"))
		case "NIC":
			model.NIC = append(model.NIC, metal3.NIC{Name: e.Name, SpeedGbps: int(number(e.Attributes, "SpeedMbps") / 1000)})
		case "Disk":
			kind := metal3.HDD
			if text(e.Attributes, "MediaType") == "SSD" {
				kind = metal3.SSD
			}
			model.Storage = append(model.Storage, metal3.Storage{Name: e.Name, Type: kind, SizeBytes: metal3.Capacity(number(e.Attributes, "CapacityBytes")), Model: text(e.Attributes, "Model"), Vendor: text(e.Attributes, "Manufacturer")})
		}
	}
	raw, err := json.Marshal(model)
	if err != nil {
		return hardware.Metal3Inventory{}, err
	}
	return hardware.DecodeMetal3Hardware(raw)
}
