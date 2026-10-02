package hardware

import (
	"encoding/json"
	"ops-platform/internal/upstream/metal3"
)

type Metal3Inventory struct {
	Hardware metal3.HardwareDetails `json:"hardware"`
	State    string                 `json:"state"`
	Partial  bool                   `json:"partial"`
}

func DecodeMetal3Hardware(raw []byte) (Metal3Inventory, error) {
	var hardware metal3.HardwareDetails
	if err := json.Unmarshal(raw, &hardware); err != nil {
		return Metal3Inventory{}, err
	}
	return Metal3Inventory{Hardware: hardware, State: "unknown", Partial: hardware.SystemVendor.Manufacturer == "" || hardware.RAMMebibytes == 0 || hardware.CPU.Count == 0 || len(hardware.Storage) == 0}, nil
}
