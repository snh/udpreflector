package reflector

import (
	"net/netip"
	"sync"
	"time"
)

type peerRegistry struct {
	mu          sync.Mutex
	timeout     time.Duration
	maxPeers    int
	hubEndpoint netip.AddrPort
	peers       map[netip.AddrPort]time.Time
}

type admission struct {
	accepted   bool
	joined     bool
	recipients []netip.AddrPort
	expired    []netip.AddrPort
	active     int
}

func newPeerRegistry(timeout time.Duration, maxPeers int, hubEndpoint netip.AddrPort) *peerRegistry {
	return &peerRegistry{
		timeout:     timeout,
		maxPeers:    maxPeers,
		hubEndpoint: canonicalAddrPort(hubEndpoint),
		peers:       make(map[netip.AddrPort]time.Time),
	}
}

func (registry *peerRegistry) admit(sender netip.AddrPort, now time.Time) admission {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	sender = canonicalAddrPort(sender)
	expired := registry.pruneLocked(now)
	if registry.hubEndpoint.IsValid() && sender == registry.hubEndpoint {
		recipients := make([]netip.AddrPort, 0, len(registry.peers))
		for peer := range registry.peers {
			recipients = append(recipients, peer)
		}
		return admission{
			accepted:   true,
			recipients: recipients,
			expired:    expired,
			active:     len(registry.peers),
		}
	}
	_, known := registry.peers[sender]
	if !known && len(registry.peers) >= registry.maxPeers {
		return admission{expired: expired, active: len(registry.peers)}
	}

	registry.peers[sender] = now
	if registry.hubEndpoint.IsValid() {
		return admission{
			accepted:   true,
			joined:     !known,
			recipients: []netip.AddrPort{registry.hubEndpoint},
			expired:    expired,
			active:     len(registry.peers),
		}
	}
	recipients := make([]netip.AddrPort, 0, len(registry.peers)-1)
	for peer := range registry.peers {
		if peer != sender {
			recipients = append(recipients, peer)
		}
	}

	return admission{
		accepted:   true,
		joined:     !known,
		recipients: recipients,
		expired:    expired,
		active:     len(registry.peers),
	}
}

func (registry *peerRegistry) expire(now time.Time) (expired []netip.AddrPort, active int) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	expired = registry.pruneLocked(now)
	return expired, len(registry.peers)
}

func (registry *peerRegistry) pruneLocked(now time.Time) []netip.AddrPort {
	var expired []netip.AddrPort
	for peer, lastSeen := range registry.peers {
		if !lastSeen.Add(registry.timeout).After(now) {
			delete(registry.peers, peer)
			expired = append(expired, peer)
		}
	}
	return expired
}

func canonicalAddrPort(endpoint netip.AddrPort) netip.AddrPort {
	if !endpoint.IsValid() {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(endpoint.Addr().Unmap(), endpoint.Port())
}
