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

type peerSnapshot struct {
	endpoint netip.AddrPort
	lastSeen time.Time
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
	if registry.hasStaticHub() && sender == registry.hubEndpoint {
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
	if !known && registry.atCapacity(sender) {
		return admission{expired: expired, active: len(registry.peers)}
	}

	registry.peers[sender] = now
	if registry.hasStaticHub() {
		return admission{
			accepted:   true,
			joined:     !known,
			recipients: []netip.AddrPort{registry.hubEndpoint},
			expired:    expired,
			active:     len(registry.peers),
		}
	}
	if registry.hasDynamicHub() {
		recipients := make([]netip.AddrPort, 0, len(registry.peers)-1)
		if sender.Addr() == registry.hubEndpoint.Addr() {
			for peer := range registry.peers {
				if peer.Addr() != registry.hubEndpoint.Addr() {
					recipients = append(recipients, peer)
				}
			}
		} else {
			for peer := range registry.peers {
				if peer.Addr() == registry.hubEndpoint.Addr() {
					recipients = append(recipients, peer)
				}
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

func (registry *peerRegistry) hasStaticHub() bool {
	return registry.hubEndpoint.IsValid() && registry.hubEndpoint.Port() != 0
}

func (registry *peerRegistry) hasDynamicHub() bool {
	return registry.hubEndpoint.IsValid() && registry.hubEndpoint.Port() == 0
}

func (registry *peerRegistry) isDynamicHubPeer(peer netip.AddrPort) bool {
	return registry.hasDynamicHub() && canonicalAddrPort(peer).Addr() == registry.hubEndpoint.Addr()
}

func (registry *peerRegistry) atCapacity(sender netip.AddrPort) bool {
	if len(registry.peers) >= registry.maxPeers {
		return true
	}
	if !registry.hasDynamicHub() || sender.Addr() == registry.hubEndpoint.Addr() {
		return false
	}

	spokes := 0
	for peer := range registry.peers {
		if peer.Addr() != registry.hubEndpoint.Addr() {
			spokes++
		}
	}
	return spokes >= registry.maxPeers-1
}

func (registry *peerRegistry) expire(now time.Time) (expired []netip.AddrPort, active int) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	expired = registry.pruneLocked(now)
	return expired, len(registry.peers)
}

func (registry *peerRegistry) snapshot(now time.Time) (peers []peerSnapshot, expired []netip.AddrPort) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	expired = registry.pruneLocked(now)
	peers = make([]peerSnapshot, 0, len(registry.peers))
	for endpoint, lastSeen := range registry.peers {
		peers = append(peers, peerSnapshot{endpoint: endpoint, lastSeen: lastSeen})
	}
	return peers, expired
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
