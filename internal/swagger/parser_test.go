package swagger

import (
	"path/filepath"
	"testing"
)

func TestParseOpenAPI_Petstore(t *testing.T) {
	tests := []struct {
		name          string
		specPath      string
		wantCount     int
		wantEndpoints []Endpoint
	}{
		{
			name:      "petstore openapi spec",
			specPath:  filepath.Join("testdata", "petstore.yaml"),
			wantCount: 3,
			wantEndpoints: []Endpoint{
				{Method: "GET", Path: "/pets", OperationID: "listPets"},
				{Method: "POST", Path: "/pets", OperationID: "createPets"},
				{Method: "GET", Path: "/pets/{id}", OperationID: "showPetById"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoints, err := ParseOpenAPI(tt.specPath)
			if err != nil {
				t.Fatalf("ParseOpenAPI() error = %v", err)
			}

			if len(endpoints) != tt.wantCount {
				t.Fatalf("expected %d endpoints, got %d", tt.wantCount, len(endpoints))
			}

			for _, want := range tt.wantEndpoints {
				if !containsEndpoint(endpoints, want) {
					t.Errorf("expected endpoint %+v not found", want)
				}
			}
		})
	}
}

func TestParseOpenAPI_InvalidFile(t *testing.T) {
	_, err := ParseOpenAPI("testdata/does_not_exist.yaml")
	if err == nil {
		t.Fatal("expected error for non-existent spec file, got nil")
	}
}

func containsEndpoint(list []Endpoint, target Endpoint) bool {
	for _, e := range list {
		if e.Method == target.Method &&
			e.Path == target.Path &&
			e.OperationID == target.OperationID {
			return true
		}
	}
	return false
}
