package api

import "embed"

// Schemas contains the versioned public JSON Schema contracts shipped with the platform.
//
//go:embed schemas events mcp/tools
var Schemas embed.FS
