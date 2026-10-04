package contract_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type openAPIDocument struct {
	Version    string                          `yaml:"openapi"`
	Paths      map[string]map[string]operation `yaml:"paths"`
	Components componentSet                    `yaml:"components"`
}

type operation struct {
	OperationID string              `yaml:"operationId"`
	Parameters  []parameter         `yaml:"parameters"`
	Responses   map[string]response `yaml:"responses"`
	RequestBody *struct {
		Content map[string]struct {
			Schema map[string]any `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"requestBody"`
	Idempotency  string `yaml:"x-idempotency"`
	SSEEventType string `yaml:"x-sse-event-envelope"`
}

type parameter struct {
	Name          string         `yaml:"name"`
	In            string         `yaml:"in"`
	AllowReserved *bool          `yaml:"allowReserved"`
	Schema        map[string]any `yaml:"schema"`
}

type response struct {
	Content map[string]mediaType `yaml:"content"`
}

type mediaType struct {
	Schema map[string]any `yaml:"schema"`
}

type componentSet struct {
	Schemas map[string]struct {
		Type       string         `yaml:"type"`
		Format     string         `yaml:"format"`
		Properties map[string]any `yaml:"properties"`
		Required   []string       `yaml:"required"`
	} `yaml:"schemas"`
}

func TestOpenAPIConventions(t *testing.T) {
	path := filepath.Join("..", "..", "api", "openapi", "platform-v1.yaml")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read OpenAPI source %s: %v", path, err)
	}

	var document openAPIDocument
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("parse OpenAPI YAML: %v", err)
	}
	if document.Version != "3.1.0" {
		t.Fatalf("OpenAPI version = %q, want 3.1.0", document.Version)
	}
	if len(document.Paths) == 0 {
		t.Fatal("OpenAPI document has no paths")
	}

	requiredRoutes := map[string]bool{
		"GET /api/v1/overview":                                        false,
		"GET /api/v1/capabilities":                                    false,
		"GET /api/v1/resources":                                       false,
		"GET /api/v1/resources/by-canonical-id":                       false,
		"GET /api/v1/resources/neighbors":                             false,
		"GET /api/v1/resources/impact-scope":                          false,
		"POST /api/v1/diagnostic-graphs:build":                        false,
		"POST /api/v1/findings:ingest":                                false,
		"GET /api/v1/findings":                                        false,
		"GET /api/v1/findings/{findingId}":                            false,
		"GET /api/v1/incidents":                                       false,
		"GET /api/v1/incidents/{incidentId}":                          false,
		"POST /api/v1/incidents/{incidentId}:transition":              false,
		"POST /api/v1/incidents:merge":                                false,
		"POST /api/v1/incidents/{incidentId}:split":                   false,
		"GET /api/v1/incidents/{incidentId}/timeline":                 false,
		"GET /api/v1/incidents/{incidentId}/evidence":                 false,
		"GET /api/v1/evidence/{evidenceId}":                           false,
		"POST /api/v1/evidence:query":                                 false,
		"GET /api/v1/incidents/{incidentId}/rca":                      false,
		"GET /api/v1/incidents/{incidentId}/rca/revisions":            false,
		"GET /api/v1/incidents/{incidentId}/rca/revisions/{revision}": false,
		"POST /api/v1/incidents/{incidentId}/investigations":          false,
		"GET /api/v1/investigations/{jobId}":                          false,
		"GET /api/v1/investigations/{jobId}/steps":                    false,
		"POST /api/v1/investigations/{jobId}:cancel":                  false,
		"GET /api/v1/investigations/{jobId}/events":                   false,
		"POST /api/v1/action-plans":                                   false,
		"GET /api/v1/action-plans/{actionPlanId}":                     false,
		"POST /api/v1/action-plans/{actionPlanId}:accept":             false,
		"POST /api/v1/action-plans/{actionPlanId}:dismiss":            false,
		"POST /api/v1/commands:risk-assess":                           false,
		"POST /api/v1/commands:risk-acknowledge":                      false,
		"POST /api/v1/command-executions":                             false,
		"GET /api/v1/command-executions/{executionId}":                false,
		"GET /api/v1/command-executions/{executionId}/events":         false,
		"POST /api/v1/command-executions/{executionId}:cancel":        false,
		"GET /api/v1/command-executions/{executionId}/post-check":     false,
		"GET /api/v1/audit-records":                                   false,
		"GET /api/v1/audit-records/{auditId}":                         false,
		"GET /api/v1/admin/source-registrations":                      false,
		"POST /api/v1/admin/source-registrations":                     false,
		"GET /api/v1/admin/tenants":                                   false,
		"POST /api/v1/admin/tenants":                                  false,
		"GET /api/v1/admin/role-bindings":                             false,
		"POST /api/v1/admin/role-bindings":                            false,
		"GET /api/v1/admin/clusters":                                  false,
		"POST /api/v1/admin/clusters":                                 false,
		"GET /api/v1/admin/execution-profiles":                        false,
		"POST /api/v1/admin/execution-profiles":                       false,
		"GET /api/v1/admin/policy-bundles":                            false,
		"POST /api/v1/admin/policy-bundles:publish":                   false,
		"GET /api/v1/admin/recipes":                                   false,
		"GET /api/v1/admin/tools":                                     false,
		"GET /api/v1/admin/legal-holds":                               false,
		"POST /api/v1/admin/legal-holds":                              false,
	}

	writeCount := 0
	operationIDs := map[string]bool{}
	for path, methods := range document.Paths {
		if strings.Contains(strings.ToLower(path), "{canonicalid}") {
			t.Errorf("Canonical ID must not be a URL path parameter: %s", path)
		}
		for method, operation := range methods {
			route := strings.ToUpper(method) + " " + path
			if _, ok := requiredRoutes[route]; ok {
				requiredRoutes[route] = true
			}
			if operation.OperationID == "" {
				t.Errorf("%s %s has no operationId", strings.ToUpper(method), path)
			} else if operationIDs[operation.OperationID] {
				t.Errorf("operationId %q is duplicated", operation.OperationID)
			} else {
				operationIDs[operation.OperationID] = true
			}
			errorSchema := operation.Responses["default"].Content["application/json"].Schema
			sp04 := path == "/api/v1/resources" || strings.HasPrefix(path, "/api/v1/resources/") || path == "/api/v1/diagnostic-graphs:build" || path == "/api/v1/evidence:query" || path == "/api/v1/evidence/{evidenceId}" || path == "/api/v1/admin/legal-holds"
			sp05 := path == "/api/v1/findings:ingest" || path == "/api/v2/findings:ingest" || path == "/api/v1/findings" || path == "/api/v1/findings/{findingId}" || path == "/api/v1/incidents" || (strings.HasPrefix(path, "/api/v1/incidents/") && !strings.HasSuffix(path, "/investigations")) || path == "/api/v1/incidents:merge"
			sp06 := strings.Contains(path, "/investigations") || path == "/api/v1/capabilities"
			if sp04 || sp05 || sp06 {
				variants, ok := errorSchema["oneOf"].([]any)
				if !ok || len(variants) != 2 || variants[0].(map[string]any)["$ref"] != "#/components/schemas/ErrorEnvelope" || variants[1].(map[string]any)["$ref"] != "#/components/schemas/SP04ErrorEnvelopeV2" {
					t.Errorf("%s must explicitly preserve v1 auth errors and declare versioned error v2", route)
				}
				shape := document.Components.Schemas["SP04ErrorEnvelopeV2"]
				if !contains(shape.Required, "schemaVersion") {
					t.Error("SP04 error major missing required version")
				}
			} else if errorSchema["$ref"] != "#/components/schemas/ErrorEnvelope" {
				t.Errorf("%s %s must use the unified ErrorEnvelope for default errors", strings.ToUpper(method), path)
			}
			canonicalQueryFound := false
			for _, parameter := range operation.Parameters {
				name := strings.ToLower(parameter.Name)
				if parameter.In == "query" && (name == "offset" || name == "page" || name == "pagesize") {
					t.Errorf("%s %s exposes unsupported offset pagination parameter %q", strings.ToUpper(method), path, parameter.Name)
				}
				if parameter.In == "query" && (name == "canonicalid" || strings.HasSuffix(name, "canonicalid")) {
					canonicalQueryFound = true
					if parameter.AllowReserved == nil || *parameter.AllowReserved {
						t.Errorf("%s %s canonicalId query parameter must set allowReserved: false", strings.ToUpper(method), path)
					}
					canonicalFormat := parameter.Schema["format"]
					if parameter.Schema["$ref"] == "#/components/schemas/CanonicalID" {
						canonicalFormat = document.Components.Schemas["CanonicalID"].Format
					}
					if canonicalFormat != "canonical-id" {
						t.Errorf("%s %s canonicalId query parameter must use canonical-id format", strings.ToUpper(method), path)
					}
				}
			}
			if strings.Contains(path, "by-canonical-id") || strings.HasSuffix(path, "/neighbors") || strings.HasSuffix(path, "/impact-scope") {
				if !canonicalQueryFound {
					t.Errorf("%s %s must accept Canonical ID in a query parameter", strings.ToUpper(method), path)
				}
			}

			if method != "get" && method != "head" && method != "options" {
				writeCount++
				hasIdempotencyKey := false
				for _, parameter := range operation.Parameters {
					if parameter.In == "header" && strings.EqualFold(parameter.Name, "Idempotency-Key") {
						hasIdempotencyKey = true
					}
				}
				if !hasIdempotencyKey && operation.Idempotency != "non-idempotent" {
					t.Errorf("%s %s must declare Idempotency-Key or x-idempotency: non-idempotent", strings.ToUpper(method), path)
				}
			}
		}
	}
	for route, found := range requiredRoutes {
		if !found {
			t.Errorf("OpenAPI document is missing frozen route %s", route)
		}
	}
	if writeCount == 0 {
		t.Fatal("OpenAPI document has no write operations")
	}

	canonicalID, ok := document.Components.Schemas["CanonicalID"]
	if !ok || canonicalID.Type != "string" || canonicalID.Properties != nil {
		t.Fatal("components.schemas.CanonicalID must be a string schema")
	}
	if canonicalID.Format != "canonical-id" {
		t.Fatalf("CanonicalID format = %q, want canonical-id", canonicalID.Format)
	}
	errorEnvelope, ok := document.Components.Schemas["ErrorEnvelope"]
	if !ok {
		t.Fatal("components.schemas.ErrorEnvelope is required")
	}
	for _, field := range []string{"code", "message", "requestId", "retryable", "details"} {
		if _, ok := errorEnvelope.Properties[field]; !ok {
			t.Errorf("ErrorEnvelope is missing %q", field)
		}
	}
	for _, required := range []string{"code", "message", "requestId", "retryable"} {
		if !contains(errorEnvelope.Required, required) {
			t.Errorf("ErrorEnvelope must require %q", required)
		}
	}
	for path, methods := range document.Paths {
		for method, operation := range methods {
			if !strings.HasSuffix(path, "/events") {
				continue
			}
			if _, ok := operation.Responses["200"].Content["text/event-stream"]; !ok {
				t.Errorf("%s %s must return text/event-stream", strings.ToUpper(method), path)
			}
			if !strings.HasPrefix(operation.SSEEventType, "#/components/schemas/") {
				t.Errorf("%s %s must reference its SSE event envelope", strings.ToUpper(method), path)
			} else {
				name := strings.TrimPrefix(operation.SSEEventType, "#/components/schemas/")
				if _, ok := document.Components.Schemas[name]; !ok {
					t.Errorf("%s %s references missing SSE envelope schema %q", strings.ToUpper(method), path, name)
				}
			}
		}
	}
}

func TestCheckGeneratedCoversGoAndTypeScriptOutput(t *testing.T) {
	makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	for _, directory := range []string{"gen", "web/src/api/generated"} {
		if !strings.Contains(string(makefile), directory) {
			t.Errorf("check-generated does not inspect %s", directory)
		}
	}
}

func TestSourceScopeContractPreservesLegacyCallsAndDisablesQueryDeclarations(t *testing.T) {
	contents, err := os.ReadFile("../../api/openapi/platform-v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document openAPIDocument
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	request := document.Components.Schemas["SourceRegistrationRequest"]
	for _, field := range []string{"backendLogicalId", "dataScopeMapping"} {
		if _, ok := request.Properties[field]; !ok || contains(request.Required, field) {
			t.Fatalf("scope declaration %s is absent or breaks legacy request compatibility", field)
		}
	}
	if _, ok := document.Components.Schemas["SourceDataScopeMapping"].Properties["verification"]; ok {
		t.Fatal("caller may claim scope verification")
	}
	registration := document.Components.Schemas["SourceRegistration"]
	capability := registration.Properties["queryCapability"].(map[string]any)
	if capability["readOnly"] != true {
		t.Fatal("query capability is writable")
	}
	for path, methods := range document.Paths {
		if !strings.HasPrefix(path, "/api/v1/admin/source-registrations") {
			continue
		}
		for method, operation := range methods {
			for status, response := range operation.Responses {
				if !strings.HasPrefix(status, "2") {
					continue
				}
				want := "#/components/schemas/SourceRegistrationEnvelope"
				if method == "get" {
					want = "#/components/schemas/SourceRegistrationPageEnvelope"
				}
				if response.Content["application/json"].Schema["$ref"] != want {
					t.Fatalf("%s %s omits typed source response", method, path)
				}
			}
		}
	}
}

func TestCheckGeneratedUsesWorkingFilesAndRejectsGeneratorDrift(t *testing.T) {
	tests := []struct {
		name             string
		directory        string
		staged           bool
		generatedDrift   bool
		extraFile        bool
		generatorFailure bool
		workspaceVisible bool
		tracked          bool
		wantPass         bool
	}{
		{name: "staged generated artifact", staged: true, wantPass: true},
		{name: "untracked generated artifact", wantPass: true},
		{name: "modified tracked generated artifact", tracked: true, wantPass: true},
		{name: "SQLC generator drift", directory: "internal/persistence/dbgen", tracked: true, generatedDrift: true, wantPass: false},
		{name: "SQLC untracked artifact", directory: "internal/persistence/dbgen", wantPass: true},
		{name: "new generator artifact", generatedDrift: true, wantPass: false},
		{name: "tracked generator drift", tracked: true, generatedDrift: true, wantPass: false},
		{name: "extraneous output", extraFile: true, wantPass: false},
		{name: "failed generator restores input", tracked: true, generatorFailure: true, wantPass: false},
		{name: "concurrent workspace retains output", tracked: true, workspaceVisible: true, wantPass: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			directory := test.directory
			if directory == "" {
				directory = "gen"
			}
			makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
			if err != nil {
				t.Fatalf("read Makefile: %v", err)
			}
			writeFile := func(name, contents string) {
				t.Helper()
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("create %s parent: %v", name, err)
				}
				if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			writeFile("Makefile", string(makefile))
			checker, err := os.ReadFile(filepath.Join("..", "..", "scripts", "check-generated.py"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile("scripts/check-generated.py", string(checker))
			writeFile("go.mod", "module check-generated-fixture\n\ngo 1.27.1\n")
			writeFile("fixture.go", "package fixture\n")
			if test.tracked {
				writeFile(directory+"/tracked.txt", "baseline\n")
			}
			runGit := func(args ...string) []byte {
				t.Helper()
				command := exec.Command("git", args...)
				command.Dir = root
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, output)
				}
				return output
			}
			runGit("init", "--quiet")
			runGit("config", "user.name", "SP-01 contract test")
			runGit("config", "user.email", "sp01-contract-test@example.invalid")
			runGit("add", "Makefile", "go.mod", "fixture.go")
			if test.tracked {
				runGit("add", directory+"/tracked.txt")
			}
			runGit("commit", "--quiet", "-m", "baseline")

			if !test.tracked {
				writeFile(directory+"/review-probe.txt", "generated\n")
				if test.staged {
					runGit("add", directory+"/review-probe.txt")
				}
			} else {
				writeFile(directory+"/tracked.txt", "changed\n")
			}

			artifact, contents := "review-probe.txt", "generated\n"
			if test.tracked {
				artifact, contents = "tracked.txt", "changed\n"
			}
			if test.generatedDrift {
				contents = "generated-drift\n"
			}
			if test.extraFile {
				artifact = "actual-output.txt"
			}
			writeFile("fixture.go", "package fixture\n//go:generate sh -c \"mkdir -p "+directory+"; printf '"+strings.TrimSuffix(contents, "\n")+"\\n' > "+directory+"/"+artifact+"\"\n")

			if test.workspaceVisible {
				originalPath := filepath.Join(root, directory, artifact)
				writeFile("fixture.go", "package fixture\n//go:generate sh -c \"test -f '"+originalPath+"' || exit 1; mkdir -p "+directory+"; printf 'changed\\n' > "+directory+"/"+artifact+"\"\n")
			}
			if test.generatorFailure {
				writeFile("fixture.go", "package fixture\n//go:generate sh -c \"exit 1\"\n")
			}
			command := exec.Command("make", "check-generated")
			command.Dir = root
			output, err := command.CombinedOutput()
			if test.wantPass && err != nil {
				t.Fatalf("make check-generated failed: %v\n%s", err, output)
			}
			if !test.wantPass && err == nil {
				t.Fatalf("make check-generated unexpectedly passed\n%s", output)
			}
			original, want := "review-probe.txt", "generated\n"
			if test.tracked {
				original, want = "tracked.txt", "changed\n"
			}
			got, readErr := os.ReadFile(filepath.Join(root, directory, original))
			if readErr != nil || string(got) != want {
				t.Fatalf("checker changed reviewable input: %q %v", got, readErr)
			}
		})
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
