package application

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"udpreflector/internal/reflector"
)

type staticReflectorPeersSource struct {
	reflectorPeers []reflector.ReflectorPeers
}

func (source staticReflectorPeersSource) ReflectorPeers() []reflector.ReflectorPeers {
	return source.reflectorPeers
}

func TestPeersHandlerListsActivePeers(t *testing.T) {
	lastSeen := time.Date(2026, time.July, 20, 12, 34, 56, 0, time.UTC)
	handler := newPeersHandler(staticReflectorPeersSource{reflectorPeers: []reflector.ReflectorPeers{{
		ReflectorPort: 40000,
		Peers: []reflector.Peer{
			{Endpoint: netip.MustParseAddrPort("192.0.2.1:12345"), LastSeen: lastSeen, Hub: false},
			{Endpoint: netip.MustParseAddrPort("[2001:db8::1]:12345"), LastSeen: lastSeen, Hub: true},
		},
	}}})
	request := httptest.NewRequest(http.MethodGet, "/peers", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cacheControl)
	}
	var reflectorPeers []reflectorPeersResponse
	if err := json.NewDecoder(response.Body).Decode(&reflectorPeers); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(reflectorPeers) != 1 || reflectorPeers[0].ReflectorPort != 40000 {
		t.Fatalf("response = %+v, want reflector port 40000", reflectorPeers)
	}
	if len(reflectorPeers[0].Peers) != 2 {
		t.Fatalf("response contains %d peers, want 2", len(reflectorPeers[0].Peers))
	}
	if reflectorPeers[0].Peers[0].Endpoint != "192.0.2.1:12345" || !reflectorPeers[0].Peers[0].LastSeen.Equal(lastSeen) || reflectorPeers[0].Peers[0].Hub {
		t.Fatalf("first peer = %+v, want spoke last seen at %s", reflectorPeers[0].Peers[0], lastSeen)
	}
	if reflectorPeers[0].Peers[1].Endpoint != "[2001:db8::1]:12345" || !reflectorPeers[0].Peers[1].Hub {
		t.Fatalf("second peer = %+v, want hub", reflectorPeers[0].Peers[1])
	}
}
