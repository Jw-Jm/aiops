package api

import "embed"

// Schemas contains the versioned public JSON Schema contracts shipped with the platform.
//
//go:embed schemas events mcp/tools assistant-tools
var Schemas embed.FS

// AnalysisCatalog contains reviewed query semantics shipped with this binary.
// It cannot be replaced using a caller-supplied path or URL.
//
//go:embed analytics/*.json
var AnalysisCatalog embed.FS
