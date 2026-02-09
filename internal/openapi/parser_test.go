package openapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestParser_ParseFile(t *testing.T) {
	parser := NewParser()

	testdataPath := filepath.Join("testdata", "petstore.yaml")

	ctx := context.Background()
	endpoints, err := parser.ParseFile(ctx, testdataPath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	// Verify we got the expected number of endpoints
	// petstore.yaml has: GET /pets, POST /pets, GET /pets/{petId}, DELETE /pets/{petId},
	// GET /pets/{petId}/photos, GET /health = 6 endpoints
	expectedCount := 6
	if len(endpoints) != expectedCount {
		t.Errorf("expected %d endpoints, got %d", expectedCount, len(endpoints))
		for _, ep := range endpoints {
			t.Logf("  %s %s", ep.Method, ep.Path)
		}
	}

	// Verify specific endpoints exist
	endpointMap := make(map[string]Endpoint)
	for _, ep := range endpoints {
		key := ep.Method + " " + ep.Path
		endpointMap[key] = ep
	}

	tests := []struct {
		method      string
		path        string
		operationID string
		hasBody     bool
	}{
		{"GET", "/pets", "listPets", false},
		{"POST", "/pets", "createPet", true},
		{"GET", "/pets/{petId}", "getPet", false},
		{"DELETE", "/pets/{petId}", "deletePet", false},
		{"GET", "/health", "healthCheck", false},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			key := tc.method + " " + tc.path
			ep, ok := endpointMap[key]
			if !ok {
				t.Fatalf("endpoint %s not found", key)
			}

			if ep.OperationID != tc.operationID {
				t.Errorf("expected operationId %s, got %s", tc.operationID, ep.OperationID)
			}

			if ep.HasBody != tc.hasBody {
				t.Errorf("expected hasBody=%v, got %v", tc.hasBody, ep.HasBody)
			}
		})
	}
}

func TestParser_ParseFile_PathParameters(t *testing.T) {
	parser := NewParser()
	testdataPath := filepath.Join("testdata", "petstore.yaml")

	ctx := context.Background()
	endpoints, err := parser.ParseFile(ctx, testdataPath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	// Find the GET /pets/{petId} endpoint
	var petEndpoint *Endpoint
	for i, ep := range endpoints {
		if ep.Method == http.MethodGet && ep.Path == "/pets/{petId}" {
			petEndpoint = &endpoints[i]
			break
		}
	}

	if petEndpoint == nil {
		t.Fatal("GET /pets/{petId} endpoint not found")
	}

	// Verify path parameter was extracted
	var hasPathParam bool
	for _, p := range petEndpoint.Parameters {
		if p.Name == "petId" && p.In == "path" {
			hasPathParam = true
			if !p.Required {
				t.Error("path parameter should be required")
			}
			if p.Type != "integer" {
				t.Errorf("expected type integer, got %s", p.Type)
			}
		}
	}

	if !hasPathParam {
		t.Error("petId path parameter not found")
	}
}

func TestParser_ParseFile_NotFound(t *testing.T) {
	parser := NewParser()

	ctx := context.Background()
	_, err := parser.ParseFile(ctx, "/nonexistent/path.yaml")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestParser_ParseFile_InvalidSpec(t *testing.T) {
	parser := NewParser()

	// Create a temporary invalid spec file
	tmpDir := t.TempDir()
	invalidPath := filepath.Join(tmpDir, "invalid.yaml")

	invalidContent := []byte("not: valid: openapi: spec")
	if err := os.WriteFile(invalidPath, invalidContent, 0o644); err != nil {
		t.Fatalf("failed to write invalid spec: %v", err)
	}

	ctx := context.Background()
	_, err := parser.ParseFile(ctx, invalidPath)
	if err == nil {
		t.Error("expected error for invalid spec")
	}
}

func TestResolvePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		params   []Parameter
		values   map[string]string
		expected string
	}{
		{
			name:     "no parameters",
			path:     "/pets",
			params:   nil,
			values:   nil,
			expected: "/pets",
		},
		{
			name: "single parameter with value",
			path: "/pets/{petId}",
			params: []Parameter{
				{Name: "petId", In: "path", Type: "integer"},
			},
			values:   map[string]string{"petId": "123"},
			expected: "/pets/123",
		},
		{
			name: "single parameter with default (integer)",
			path: "/pets/{petId}",
			params: []Parameter{
				{Name: "petId", In: "path", Type: "integer"},
			},
			values:   nil,
			expected: "/pets/1",
		},
		{
			name: "single parameter with default (string)",
			path: "/users/{username}",
			params: []Parameter{
				{Name: "username", In: "path", Type: "string"},
			},
			values:   nil,
			expected: "/users/test",
		},
		{
			name: "multiple parameters",
			path: "/users/{userId}/posts/{postId}",
			params: []Parameter{
				{Name: "userId", In: "path", Type: "integer"},
				{Name: "postId", In: "path", Type: "integer"},
			},
			values:   map[string]string{"userId": "42"},
			expected: "/users/42/posts/1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ResolvePath(tc.path, tc.params, tc.values)
			if result != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}
