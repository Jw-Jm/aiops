package datascope

type Mapping struct {
	NativeTenant   string              `json:"nativeTenant,omitempty"`
	Scopes         map[string][]string `json:"scopes,omitempty"`
	RequiredLabels map[string]string   `json:"requiredLabels,omitempty"`
}
