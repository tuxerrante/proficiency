// Package openapi provides OpenAPI specification parsing functionality.
// It extracts endpoint definitions from OpenAPI 3.0 specs for use in load testing.
package openapi

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Common string constants to avoid magic strings (goconst).
const (
	methodGet          = "GET"
	defaultStringValue = "test"
	paramInPath        = "path"
	typeInteger        = "integer"
)

// Endpoint represents a single API endpoint extracted from the OpenAPI spec.
// It contains all necessary information to construct HTTP requests during load testing.
type Endpoint struct {
	Method      string      // HTTP method (GET, POST, PUT, DELETE, etc.)
	Path        string      // URL path pattern (e.g., "/users/{id}")
	OperationID string      // Unique operation identifier from the spec
	Parameters  []Parameter // Path, query, and header parameters
	ContentType string      // JSON request content type when payload generation succeeds
	Body        []byte      // Generated JSON request payload when requestBody synthesis succeeds
	HasBody     bool        // Whether the endpoint expects a request body
	Tags        []string    // Grouping tags from the spec
}

// Parameter represents a single parameter definition from the OpenAPI spec.
type Parameter struct {
	Name     string // Parameter name
	In       string // Location: "path", "query", "header", "cookie"
	Required bool   // Whether the parameter is required
	Type     string // Data type (string, integer, etc.)
	Example  any    // Example value from the spec, if available
}

// Parser handles OpenAPI specification parsing.
type Parser struct {
	loader *openapi3.Loader
}

// NewParser creates a new OpenAPI parser with default configuration.
// The parser is configured to resolve external references if encountered.
func NewParser() *Parser {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	return &Parser{loader: loader}
}

// ParseFile reads and parses an OpenAPI specification from the given file path.
// It validates the spec structure and extracts all endpoint definitions.
func (p *Parser) ParseFile(ctx context.Context, path string) ([]Endpoint, error) {
	doc, err := p.loader.LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("loading OpenAPI spec from %s: %w", path, err)
	}

	if err := doc.Validate(ctx); err != nil {
		return nil, fmt.Errorf("validating OpenAPI spec: %w", err)
	}

	return p.extractEndpoints(doc)
}

// extractEndpoints iterates through all paths and operations in the spec,
// building a slice of Endpoint structs.
func (p *Parser) extractEndpoints(doc *openapi3.T) ([]Endpoint, error) {
	var endpoints []Endpoint

	for path, pathItem := range doc.Paths.Map() {
		ops := map[string]*openapi3.Operation{
			methodGet: pathItem.Get,
			"POST":    pathItem.Post,
			"PUT":     pathItem.Put,
			"DELETE":  pathItem.Delete,
			"PATCH":   pathItem.Patch,
			"HEAD":    pathItem.Head,
			"OPTIONS": pathItem.Options,
		}

		for method, op := range ops {
			if op == nil {
				continue
			}

			endpoint := Endpoint{
				Method:      method,
				Path:        path,
				OperationID: op.OperationID,
				Tags:        op.Tags,
				HasBody:     op.RequestBody != nil,
			}

			// Extract parameters from operation and path item
			endpoint.Parameters = p.extractParameters(op.Parameters, pathItem.Parameters)

			if op.RequestBody != nil {
				body, contentType, err := p.extractRequestBody(op.RequestBody)
				if err != nil {
					return nil, fmt.Errorf("extracting request body for %s %s: %w", method, path, err)
				}
				endpoint.Body = body
				endpoint.ContentType = contentType
			}

			endpoints = append(endpoints, endpoint)
		}
	}

	return endpoints, nil
}

// extractParameters merges operation-level and path-level parameters.
// Operation parameters take precedence over path parameters with the same name.
func (p *Parser) extractParameters(opParams, pathParams openapi3.Parameters) []Parameter {
	paramMap := make(map[string]Parameter)

	// Add path-level parameters first
	for _, ref := range pathParams {
		if ref.Value == nil {
			continue
		}
		param := p.convertParameter(ref.Value)
		paramMap[param.Name+":"+param.In] = param
	}

	// Operation parameters override path parameters
	for _, ref := range opParams {
		if ref.Value == nil {
			continue
		}
		param := p.convertParameter(ref.Value)
		paramMap[param.Name+":"+param.In] = param
	}

	params := make([]Parameter, 0, len(paramMap))
	for _, param := range paramMap {
		params = append(params, param)
	}
	return params
}

// convertParameter transforms an OpenAPI parameter into our internal representation.
func (p *Parser) convertParameter(param *openapi3.Parameter) Parameter {
	result := Parameter{
		Name:     param.Name,
		In:       param.In,
		Required: param.Required,
	}

	if param.Schema != nil && param.Schema.Value != nil {
		if types := param.Schema.Value.Type.Slice(); len(types) > 0 {
			result.Type = types[0]
		}
		result.Example = param.Schema.Value.Example
	}

	return result
}

func (p *Parser) extractRequestBody(requestBodyRef *openapi3.RequestBodyRef) ([]byte, string, error) {
	if requestBodyRef == nil || requestBodyRef.Value == nil || len(requestBodyRef.Value.Content) == 0 {
		return nil, "", nil
	}

	for _, contentType := range jsonContentTypes(requestBodyRef.Value.Content) {
		mediaType := requestBodyRef.Value.Content[contentType]
		body, ok, err := buildJSONBody(mediaType)
		if err != nil {
			return nil, "", fmt.Errorf("building JSON body for %s: %w", contentType, err)
		}
		if ok {
			return body, contentType, nil
		}
	}

	// Unsupported content types are skipped intentionally.
	return nil, "", nil
}

func jsonContentTypes(content openapi3.Content) []string {
	contentTypes := make([]string, 0, len(content))
	for contentType := range content {
		if IsJSONContentType(contentType) {
			contentTypes = append(contentTypes, contentType)
		}
	}

	sort.Strings(contentTypes)
	for i, contentType := range contentTypes {
		if strings.EqualFold(strings.TrimSpace(contentType), contentTypeApplicationJSON) {
			contentTypes[0], contentTypes[i] = contentTypes[i], contentTypes[0]
			break
		}
	}

	return contentTypes
}

func buildJSONBody(mediaType *openapi3.MediaType) ([]byte, bool, error) {
	value, ok := jsonExampleValue(mediaType)
	if !ok {
		value, ok = schemaFallbackValue(mediaType)
		if !ok {
			return nil, false, nil
		}
	}

	body, err := json.Marshal(value)
	if err != nil {
		return nil, false, fmt.Errorf("marshalling JSON body: %w", err)
	}

	return body, true, nil
}

func jsonExampleValue(mediaType *openapi3.MediaType) (any, bool) {
	if mediaType == nil {
		return nil, false
	}
	if mediaType.Example != nil {
		return mediaType.Example, true
	}

	if len(mediaType.Examples) > 0 {
		keys := make([]string, 0, len(mediaType.Examples))
		for key := range mediaType.Examples {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		for _, key := range keys {
			exampleRef := mediaType.Examples[key]
			if exampleRef != nil && exampleRef.Value != nil && exampleRef.Value.Value != nil {
				return exampleRef.Value.Value, true
			}
		}
	}

	if mediaType.Schema != nil && mediaType.Schema.Value != nil && mediaType.Schema.Value.Example != nil {
		return mediaType.Schema.Value.Example, true
	}

	return nil, false
}

func schemaFallbackValue(mediaType *openapi3.MediaType) (any, bool) {
	if mediaType == nil || mediaType.Schema == nil || mediaType.Schema.Value == nil {
		return nil, false
	}

	return valueFromSchema(mediaType.Schema.Value)
}

func valueFromSchema(schema *openapi3.Schema) (any, bool) {
	return valueFromSchemaRecursive(schema, map[*openapi3.Schema]struct{}{})
}

func valueFromSchemaRecursive(schema *openapi3.Schema, visited map[*openapi3.Schema]struct{}) (any, bool) {
	if schema == nil {
		return nil, false
	}
	if _, seen := visited[schema]; seen {
		// Recursive references are skipped to keep payload generation bounded.
		return nil, false
	}
	visited[schema] = struct{}{}
	defer delete(visited, schema)

	if schema.Example != nil {
		return schema.Example, true
	}
	if schema.Default != nil {
		return schema.Default, true
	}

	if len(schema.OneOf) > 0 {
		return firstCompositeSchemaValue(schema.OneOf, visited)
	}
	if len(schema.AnyOf) > 0 {
		return firstCompositeSchemaValue(schema.AnyOf, visited)
	}
	if len(schema.AllOf) > 0 {
		return firstCompositeSchemaValue(schema.AllOf, visited)
	}

	if len(schema.Properties) > 0 {
		return objectFromSchema(schema, visited), true
	}

	types := schema.Type.Slice()
	if len(types) == 0 {
		return nil, false
	}

	return valueFromSchemaType(types[0], schema, visited)
}

func firstCompositeSchemaValue(schemaRefs openapi3.SchemaRefs, visited map[*openapi3.Schema]struct{}) (any, bool) {
	for _, schemaRef := range schemaRefs {
		if schemaRef == nil || schemaRef.Value == nil {
			continue
		}
		if value, ok := valueFromSchemaRecursive(schemaRef.Value, visited); ok {
			return value, true
		}
	}

	return nil, false
}

func valueFromSchemaType(schemaType string, schema *openapi3.Schema, visited map[*openapi3.Schema]struct{}) (any, bool) {
	switch schemaType {
	case "object":
		return objectFromSchema(schema, visited), true
	case "array":
		if schema.Items == nil || schema.Items.Value == nil {
			return []any{}, true
		}

		item, ok := valueFromSchemaRecursive(schema.Items.Value, visited)
		if !ok {
			return []any{}, true
		}

		return []any{item}, true
	case "string":
		if len(schema.Enum) > 0 {
			return schema.Enum[0], true
		}
		return defaultStringValue, true
	case typeInteger:
		if len(schema.Enum) > 0 {
			return schema.Enum[0], true
		}
		return 1, true
	case "number":
		if len(schema.Enum) > 0 {
			return schema.Enum[0], true
		}
		return 1.0, true
	case "boolean":
		if len(schema.Enum) > 0 {
			return schema.Enum[0], true
		}
		return true, true
	default:
		return nil, false
	}
}

func objectFromSchema(schema *openapi3.Schema, visited map[*openapi3.Schema]struct{}) map[string]any {
	value := make(map[string]any, len(schema.Properties))
	for name, property := range schema.Properties {
		if property == nil || property.Value == nil {
			continue
		}

		if propertyValue, ok := valueFromSchemaRecursive(property.Value, visited); ok {
			value[name] = propertyValue
		}
	}

	return value
}

// pathParamRegex matches path parameters in OpenAPI format: {paramName}.
var pathParamRegex = regexp.MustCompile(`\{([^}]+)\}`)

// ResolvePath replaces path parameters with provided values.
// Parameters not found in the values map are replaced with placeholder values
// based on their type (e.g., "1" for integers, "test" for strings).
//
// EXAMPLE:
//
//	path: "/users/{id}/posts/{postId}"
//	values: map[string]string{"id": "123"}
//	result: "/users/123/posts/1" (postId gets default integer placeholder)
func ResolvePath(path string, params []Parameter, values map[string]string) string {
	result := path

	paramTypes := make(map[string]string)
	for _, p := range params {
		if p.In == paramInPath {
			paramTypes[p.Name] = p.Type
		}
	}

	result = pathParamRegex.ReplaceAllStringFunc(result, func(match string) string {
		paramName := strings.Trim(match, "{}")

		if val, ok := values[paramName]; ok {
			return val
		}

		// Generate placeholder based on type
		paramType := paramTypes[paramName]
		switch paramType {
		case typeInteger, "number":
			return "1"
		default:
			return defaultStringValue
		}
	})

	return result
}
