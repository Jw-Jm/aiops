package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"

	"ops-platform/api"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaPrefix = "https://ops.local/schemas/"

var schemaIDPattern = regexp.MustCompile(`^https://ops\.local/schemas/[a-z0-9._/-]+/v[1-9][0-9]*(#.*)?$`)

type schemaDocument struct {
	id  string
	doc any
}

var (
	loadOnce      sync.Once
	loadedSchemas map[string]schemaDocument
	loadErr       error

	compileMu sync.Mutex
	compiled  = make(map[string]*jsonschema.Schema)
)

// Validate validates a JSON payload against an embedded, versioned platform schema.
// Schema references are resolved only from the embedded contract set; runtime network
// access is not used to load schemas.
func Validate(schemaID string, payload []byte) error {
	baseID, _, _ := strings.Cut(schemaID, "#")
	if !schemaIDPattern.MatchString(schemaID) || !strings.HasPrefix(baseID, schemaPrefix) {
		return fmt.Errorf("invalid or unsupported schema ID %q", schemaID)
	}

	if len(payload) == 0 {
		return errors.New("schema payload is empty")
	}

	loadSchemas()
	if loadErr != nil {
		return fmt.Errorf("load embedded schemas: %w", loadErr)
	}
	if _, ok := loadedSchemas[baseID]; !ok {
		return fmt.Errorf("schema ID %q is not registered", baseID)
	}

	schema, err := compiledSchema(schemaID)
	if err != nil {
		return err
	}
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("decode payload for schema %q: %w", schemaID, err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("validate payload against schema %q: %w", schemaID, err)
	}
	if strings.Contains(schemaID, "#") {
		return nil
	}
	if err := validateTenantBindings(value); err != nil {
		return fmt.Errorf("validate tenant-bound canonical IDs for schema %q: %w", schemaID, err)
	}
	return nil
}

func loadSchemas() {
	loadOnce.Do(func() {
		loadedSchemas = make(map[string]schemaDocument)
		loadErr = fs.WalkDir(api.Schemas, ".", func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(file, ".schema.json") {
				return nil
			}
			contents, err := api.Schemas.ReadFile(file)
			if err != nil {
				return err
			}
			var doc map[string]any
			if err := json.Unmarshal(contents, &doc); err != nil {
				return fmt.Errorf("decode %s: %w", file, err)
			}
			id, ok := doc["$id"].(string)
			if !ok || !schemaIDPattern.MatchString(id) || strings.Contains(id, "#") {
				return fmt.Errorf("%s has an invalid $id", file)
			}
			if _, exists := loadedSchemas[id]; exists {
				return fmt.Errorf("duplicate schema ID %q", id)
			}
			loadedSchemas[id] = schemaDocument{id: id, doc: doc}
			return nil
		})
		if loadErr != nil {
			return
		}
		if len(loadedSchemas) == 0 {
			loadErr = errors.New("no JSON Schema documents are embedded")
		}
	})
}

func compiledSchema(schemaID string) (*jsonschema.Schema, error) {
	compileMu.Lock()
	defer compileMu.Unlock()
	if schema, ok := compiled[schemaID]; ok {
		return schema, nil
	}

	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.UseLoader(offlineSchemaLoader{})
	for id, document := range loadedSchemas {
		if err := compiler.AddResource(id, document.doc); err != nil {
			return nil, fmt.Errorf("register schema %q: %w", id, err)
		}
	}
	schema, err := compiler.Compile(schemaID)
	if err != nil {
		return nil, fmt.Errorf("compile schema %q: %w", schemaID, err)
	}
	compiled[schemaID] = schema
	return schema, nil
}

type offlineSchemaLoader struct{}

func (offlineSchemaLoader) Load(resource string) (any, error) {
	return nil, fmt.Errorf("schema resource %q is not in the embedded contract set", resource)
}

func validateTenantBindings(value any) error {
	var tenants []string
	var walk func(any, string, string) error
	walk = func(value any, inheritedTenant, location string) error {
		switch value := value.(type) {
		case map[string]any:
			currentTenant := inheritedTenant
			for _, key := range []string{"tenantId", "tenant_id"} {
				if tenant, ok := value[key].(string); ok && tenant != "" {
					currentTenant = tenant
					tenants = append(tenants, tenant)
				}
			}
			for key, nested := range value {
				if isCanonicalIDField(key) || isCanonicalIDValue(key, nested) {
					canonicalID, ok := nested.(string)
					if ok {
						tenant, err := canonicalIDTenant(canonicalID)
						if err != nil {
							return fmt.Errorf("%s.%s is invalid", location, key)
						}
						tenants = append(tenants, tenant)
						if currentTenant != "" && tenant != currentTenant {
							return fmt.Errorf("%s.%s belongs to another tenant", location, key)
						}
					}
				}
				if err := walk(nested, currentTenant, path.Join(location, key)); err != nil {
					return err
				}
			}
		case []any:
			for index, nested := range value {
				if err := walk(nested, inheritedTenant, fmt.Sprintf("%s[%d]", location, index)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(value, "", "$ "); err != nil {
		return err
	}
	if len(tenants) > 1 {
		first := tenants[0]
		for _, tenant := range tenants[1:] {
			if tenant != first {
				return errors.New("payload contains tenant references from multiple tenants")
			}
		}
	}
	return nil
}

func isCanonicalIDField(name string) bool {
	return name == "canonicalId" || strings.HasSuffix(name, "CanonicalId") || name == "canonical_id" || strings.HasSuffix(name, "_canonical_id")
}

func isCanonicalIDValue(name string, value any) bool {
	if name != "from" && name != "to" {
		return false
	}
	text, ok := value.(string)
	return ok && strings.Contains(text, "+v1://")
}

func canonicalIDTenant(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Opaque != "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("invalid Canonical ID URI")
	}
	if !strings.HasSuffix(parsed.Scheme, "+v1") {
		return "", errors.New("unsupported Canonical ID version")
	}
	segments := strings.Split(strings.TrimPrefix(parsed.EscapedPath(), "/"), "/")
	if len(segments) != 4 || segments[0] == "" || segments[2] == "" || segments[3] == "" {
		return "", errors.New("invalid Canonical ID path")
	}
	return parsed.Host, nil
}
