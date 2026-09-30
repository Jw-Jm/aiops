// Package policies exposes the reviewed SP-03 Rego policy modules.
package policies

import "embed"

// FS contains the policy modules and their local OPA tests.
//
//go:embed tool/v1/*.rego action/v1/*.rego tests/*.rego
var FS embed.FS
