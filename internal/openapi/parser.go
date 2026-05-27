// Package openapi provides OpenAPI specification parsing functionality.
// It extracts endpoint definitions from OpenAPI 3.0 specs for use in load testing.
package openapi

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Common string constants to avoid magic strings (goconst).
const (
	methodGet   = "GET"
	paramInPath = "path"
	typeInteger = "integer"
)

// Endpoint represents a single API endpoint extracted from the OpenAPI spec.
// It contains all necessary information to construct HTTP requests during load testing.
type Endpoint struct {
	Method      string      // HTTP method (GET, POST, PUT, DELETE, etc.)
	Path        string      // URL path pattern (e.g., "/users/{id}")
	OperationID string      // Unique operation identifier from the spec
	Parameters  []Parameter // Path, query, and header parameters
	ContentType string      // Request content type if applicable
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

			// Determine content type from request body if present
			if op.RequestBody != nil && op.RequestBody.Value != nil {
				for contentType := range op.RequestBody.Value.Content {
					endpoint.ContentType = contentType
					break // Take first available content type
				}
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
		result.Type = param.Schema.Value.Type.Slice()[0]
		result.Example = param.Schema.Value.Example
	}

	return result
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
			return "test"
		}
	})

	return result
}
