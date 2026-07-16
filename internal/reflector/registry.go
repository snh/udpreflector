package reflector

import (
	"net/netip"
	"sync"
	"time"
)

type peerRegistry struct {
	mu       sync.Mutex
	timeout  time.Duration
	maxPeers int
	peers    map[netip.AddrPort]time.Time
}

type admission struct {
	accepted   bool
	recipients []netip.AddrPort
	expired    int
	active     int
}

func newPeerRegistry(timeout time.Duration, maxPeers int) *peerRegistry {
	return &peerRegistry{
		timeout:  timeout,
		maxPeers: maxPeers,
		peers:    make(map[netip.AddrPort]time.Time),
	}
}

func (registry *peerRegistry) admit(sender netip.AddrPort, now time.Time) admission {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	sender = canonicalAddrPort(sender)
	expired := registry.pruneLocked(now)
	if _, known := registry.peers[sender]; !known && len(registry.peers) >= registry.maxPeers {
		return admission{expired: expired, active: len(registry.peers)}
	}

	registry.peers[sender] = now
	recipients := make([]netip.AddrPort, 0, len(registry.peers)-1)
	for peer := range registry.peers {
		if peer != sender {
			recipients = append(recipients, peer)
		}
	}

	return admission{
		accepted:   true,
		recipients: recipients,
		expired:    expired,
		active:     len(registry.peers),
	}
}

func (registry *peerRegistry) expire(now time.Time) (expired int, active int) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	expired = registry.pruneLocked(now)
	return expired, len(registry.peers)
}

func (registry *peerRegistry) pruneLocked(now time.Time) int {
	expired := 0
	for peer, lastSeen := range registry.peers {
		if !lastSeen.Add(registry.timeout).After(now) {
			delete(registry.peers, peer)
			expired++
		}
	}
	return expired
}

func canonicalAddrPort(endpoint netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(endpoint.Addr().Unmap(), endpoint.Port())
}
