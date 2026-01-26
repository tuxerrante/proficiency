package swagger

import (
	"context"
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Endpoint represents a single HTTP endpoint extracted from an OpenAPI spec.
type Endpoint struct {
	Method      string
	Path        string
	OperationID string
}

// ParseOpenAPI parses an OpenAPI 3.0 specification file and extracts all endpoints.
func ParseOpenAPI(path string) ([]Endpoint, error) {
	loader := openapi3.NewLoader()

	doc, err := loader.LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("load openapi spec: %w", err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("validate openapi spec: %w", err)
	}

	var endpoints []Endpoint

	for route, pathItem := range doc.Paths.Map() {
		if pathItem == nil {
			continue
		}

		operations := pathItem.Operations()
		for method, op := range operations {
			if op == nil {
				continue
			}

			endpoints = append(endpoints, Endpoint{
				Method:      strings.ToUpper(method),
				Path:        route,
				OperationID: op.OperationID,
			})
		}
	}

	return endpoints, nil
}
