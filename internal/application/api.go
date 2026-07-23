package application

import (
	"encoding/json"
	"net/http"
	"time"

	"udpreflector/internal/reflector"
)

type reflectorPeersSource interface {
	ReflectorPeers() []reflector.ReflectorPeers
}

type peerResponse struct {
	Endpoint string    `json:"endpoint"`
	LastSeen time.Time `json:"last_seen"`
	Hub      *bool     `json:"hub,omitempty"`
}

type reflectorPeersResponse struct {
	ReflectorPort uint16         `json:"reflector_port"`
	Peers         []peerResponse `json:"peers"`
}

func newPeersHandler(source reflectorPeersSource) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		reflectorPeers := source.ReflectorPeers()
		body := make([]reflectorPeersResponse, len(reflectorPeers))
		for reflectorIndex, reflector := range reflectorPeers {
			peers := make([]peerResponse, len(reflector.Peers))
			for peerIndex, peer := range reflector.Peers {
				var hub *bool
				if reflector.HubAndSpoke {
					hub = &peer.Hub
				}
				peers[peerIndex] = peerResponse{
					Endpoint: peer.Endpoint.String(),
					LastSeen: peer.LastSeen,
					Hub:      hub,
				}
			}
			body[reflectorIndex] = reflectorPeersResponse{ReflectorPort: reflector.ReflectorPort, Peers: peers}
		}

		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(body)
	})
}
