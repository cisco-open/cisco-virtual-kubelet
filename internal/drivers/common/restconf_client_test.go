package common

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Dummy struct for testing marshalling/unmarshalling
type testData struct {
	Name string `json:"name"`
}

func TestRestconfClient_Get(t *testing.T) {
	expectedPath := "/restconf/data/test"
	expectedResponse := `{"name":"test-item"}`

	// 1. Setup Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Method, Path, and Auth
		if r.Method != "GET" {
			t.Errorf("Expected GET, got %s", r.Method)
		}
		if r.URL.Path != expectedPath {
			t.Errorf("Expected path %s, got %s", expectedPath, r.URL.Path)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "admin" {
			t.Errorf("Basic Auth failed or missing")
		}

		w.Header().Set("Content-Type", "application/yang-data+json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, expectedResponse)
	}))
	defer server.Close()

	// 2. Initialize Client
	client := NewClientRestconfClient(server.URL, nil, nil, 5*time.Second)

	// 3. Define local unmarshaller logic
	unmarshalFn := func(data []byte, v any) error {
		return json.Unmarshal(data, v)
	}

	// 4. Execute
	var result testData
	err := client.Get(context.Background(), expectedPath, &result, unmarshalFn)

	// 5. Assert
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if result.Name != "test-item" {
		t.Errorf("Expected name test-item, got %s", result.Name)
	}
}

func TestRestconfClient_Post(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/yang-data+json" {
			t.Errorf("Missing RESTconf Content-Type header")
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := NewClientRestconfClient(server.URL, nil, nil, 5*time.Second)

	marshalFn := func(v any) ([]byte, error) {
		return json.Marshal(v)
	}

	payload := testData{Name: "new-item"}
	err := client.Post(context.Background(), "/restconf/data", payload, marshalFn)

	if err != nil {
		t.Errorf("Post failed: %v", err)
	}
}

func TestRestconfClient_Delete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("Expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClientRestconfClient(server.URL, nil, nil, 5*time.Second)

	err := client.Delete(context.Background(), "/restconf/data/item")
	if err != nil {
		t.Errorf("Delete failed: %v", err)
	}
}

func TestRestconfClient_HttpError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // 400
	}))
	defer server.Close()

	client := NewClientRestconfClient(server.URL, nil, nil, 5*time.Second)

	err := client.Get(context.Background(), "/bad", nil, nil)
	if err == nil {
		t.Error("Expected error for 400 status code, got nil")
	}
}
