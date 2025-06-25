package collector

import (
	"encoding/json"
	"testing"
)

func TestRepositoryJSONSerialization(t *testing.T) {
	repo := Repository{
		Org:  "testorg",
		Name: "test-repo",
	}

	// Test marshaling
	data, err := json.Marshal(repo)
	if err != nil {
		t.Fatalf("Failed to marshal repository: %v", err)
	}

	// Test unmarshaling
	var unmarshaled Repository
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal repository: %v", err)
	}

	// Verify fields
	if unmarshaled.Org != repo.Org {
		t.Errorf("Org = %v, want %v", unmarshaled.Org, repo.Org)
	}
	if unmarshaled.Name != repo.Name {
		t.Errorf("Name = %v, want %v", unmarshaled.Name, repo.Name)
	}
}

func TestRepositoryJSONFormat(t *testing.T) {
	repo := Repository{
		Org:  "testorg",
		Name: "test-repo",
	}

	data, err := json.Marshal(repo)
	if err != nil {
		t.Fatalf("Failed to marshal repository: %v", err)
	}

	expected := `{"org":"testorg","name":"test-repo"}`
	if string(data) != expected {
		t.Errorf("JSON = %v, want %v", string(data), expected)
	}
}
