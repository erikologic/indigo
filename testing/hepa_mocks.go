package testing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	toolsozone "github.com/bluesky-social/indigo/api/ozone"
	"github.com/bluesky-social/indigo/plc"
)

// MockOzoneServer is a mock Ozone moderation service for testing
type MockOzoneServer struct {
	server *httptest.Server
	events []toolsozone.ModerationEmitEvent_Input
	mu     sync.RWMutex
}

func NewMockOzoneServer(t *testing.T) *MockOzoneServer {
	mock := &MockOzoneServer{
		events: make([]toolsozone.ModerationEmitEvent_Input, 0),
	}

	mux := http.NewServeMux()

	// Handle ozone moderation event submission
	mux.HandleFunc("/xrpc/tools.ozone.moderation.emitEvent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var event toolsozone.ModerationEmitEvent_Input
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Logf("Failed to decode ozone event: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		mock.mu.Lock()
		mock.events = append(mock.events, event)
		mock.mu.Unlock()

		t.Logf("MockOzone: Received event (total: %d)", len(mock.events))

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":        len(mock.events),
			"createdBy": event.CreatedBy,
		})
	})

	// Handle query events (returns empty list)
	mux.HandleFunc("/xrpc/tools.ozone.moderation.queryEvents", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"events": []interface{}{},
			"cursor": "",
		})
	})

	// Handle get record (returns not found - acceptable for tests)
	mux.HandleFunc("/xrpc/tools.ozone.moderation.getRecord", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "RecordNotFound",
			"message": "Record not found",
		})
	})

	mock.server = httptest.NewServer(mux)
	t.Logf("MockOzoneServer started at %s", mock.server.URL)

	return mock
}

func (m *MockOzoneServer) Host() string {
	return m.server.URL
}

func (m *MockOzoneServer) GetEvents() []toolsozone.ModerationEmitEvent_Input {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to avoid race conditions
	events := make([]toolsozone.ModerationEmitEvent_Input, len(m.events))
	copy(events, m.events)
	return events
}

func (m *MockOzoneServer) EventCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.events)
}

func (m *MockOzoneServer) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

// TestPLCServer wraps a PLC client with an HTTP server for external process testing
type TestPLCServer struct {
	server    *httptest.Server
	plcClient plc.PLCClient
}

func NewTestPLCServer(t *testing.T, plcClient plc.PLCClient) *TestPLCServer {
	mock := &TestPLCServer{
		plcClient: plcClient,
	}

	mux := http.NewServeMux()

	// Handle DID resolution requests
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Extract DID from path (e.g., /did:plc:abc123)
		did := r.URL.Path
		if len(did) > 0 && did[0] == '/' {
			did = did[1:]
		}

		if did == "" || !strings.HasPrefix(did, "did:") {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid DID"})
			return
		}

		// Resolve DID document using the real PLC client
		doc, err := mock.plcClient.GetDocument(r.Context(), did)
		if err != nil {
			t.Logf("TestPLCServer: Failed to resolve DID %s: %v", did, err)
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "DID not found"})
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(doc)
	})

	mock.server = httptest.NewServer(mux)
	t.Logf("TestPLCServer started at %s", mock.server.URL)

	return mock
}

func (s *TestPLCServer) Host() string {
	return s.server.URL
}

func (s *TestPLCServer) Close() {
	if s.server != nil {
		s.server.Close()
	}
}
