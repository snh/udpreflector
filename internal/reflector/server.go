package reflector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"udpreflector/internal/observability"
)

const maximumUDPPacketSize = 65535

type PortRange struct {
	Start uint16
	End   uint16
}

type Settings struct {
	PortRange       PortRange
	HubEndpoint     netip.AddrPort
	PeerTimeout     time.Duration
	MaxPeersPerPort int
	UDP4Address     netip.Addr
	UDP6Address     netip.Addr
}

type Peer struct {
	Endpoint netip.AddrPort
	LastSeen time.Time
	Hub      bool
}

type ReflectorPeers struct {
	ReflectorPort uint16
	Peers         []Peer
}

type Server struct {
	metrics   *observability.Metrics
	logger    *slog.Logger
	relays    []*portRelay
	listeners []*udpListener
	closeOnce sync.Once
}

type portRelay struct {
	port     uint16
	registry *peerRegistry
	udp4     *net.UDPConn
	udp6     *net.UDPConn
}

type udpListener struct {
	port   uint16
	family string
	conn   *net.UDPConn
	relay  *portRelay
}

func NewServer(settings Settings, metrics *observability.Metrics, logger *slog.Logger) (*Server, error) {
	server := &Server{metrics: metrics, logger: logger}
	if err := validateSettings(settings); err != nil {
		return nil, err
	}
	for port := settings.PortRange.Start; ; port++ {
		hubEndpoint := mappedHubEndpoint(settings, port)
		relay := &portRelay{
			port:     port,
			registry: newPeerRegistry(settings.PeerTimeout, settings.MaxPeersPerPort, hubEndpoint),
		}

		udp4, err := listenUDP("udp4", settings.UDP4Address, port)
		if err != nil {
			server.closeListeners()
			return nil, fmt.Errorf("listen on IPv4 UDP port %d: %w", port, err)
		}
		relay.udp4 = udp4
		server.listeners = append(server.listeners, &udpListener{port: port, family: "ipv4", conn: udp4, relay: relay})

		udp6, err := listenUDP("udp6", settings.UDP6Address, port)
		if err != nil {
			server.closeListeners()
			return nil, fmt.Errorf("listen on IPv6 UDP port %d: %w", port, err)
		}
		relay.udp6 = udp6
		server.listeners = append(server.listeners, &udpListener{port: port, family: "ipv6", conn: udp6, relay: relay})
		server.relays = append(server.relays, relay)
		metrics.SetActivePeers(port, 0)

		if port == settings.PortRange.End {
			break
		}
	}
	return server, nil
}

func validateSettings(settings Settings) error {
	if settings.PortRange.Start == 0 || settings.PortRange.End < settings.PortRange.Start {
		return errors.New("invalid UDP port range")
	}
	if settings.HubEndpoint.IsValid() {
		address := settings.HubEndpoint.Addr().Unmap()
		if !address.IsValid() || address.IsUnspecified() {
			return errors.New("invalid hub endpoint")
		}
		if settings.HubEndpoint.Port() != 0 {
			lastHubPort := uint32(settings.HubEndpoint.Port()) + uint32(settings.PortRange.End-settings.PortRange.Start)
			if lastHubPort > 65535 {
				return errors.New("hub port mapping ends above 65535")
			}
		}
	}
	if settings.PeerTimeout <= 0 {
		return errors.New("peer timeout must be positive")
	}
	if settings.MaxPeersPerPort <= 0 {
		return errors.New("maximum peers per port must be positive")
	}
	if !settings.UDP4Address.Is4() {
		return errors.New("UDP4 listen address must be IPv4")
	}
	if !settings.UDP6Address.Is6() || settings.UDP6Address.Is4In6() {
		return errors.New("UDP6 listen address must be IPv6")
	}
	return nil
}

func mappedHubEndpoint(settings Settings, port uint16) netip.AddrPort {
	if !settings.HubEndpoint.IsValid() {
		return netip.AddrPort{}
	}
	if settings.HubEndpoint.Port() == 0 {
		return netip.AddrPortFrom(settings.HubEndpoint.Addr().Unmap(), 0)
	}
	offset := uint32(port - settings.PortRange.Start)
	return netip.AddrPortFrom(
		settings.HubEndpoint.Addr().Unmap(),
		uint16(uint32(settings.HubEndpoint.Port())+offset),
	)
}

func (server *Server) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errorChannel := make(chan error, len(server.listeners))
	var workers sync.WaitGroup
	for _, listener := range server.listeners {
		server.metrics.SetListenerUp(listener.port, listener.family, true)
		workers.Add(1)
		go func(listener *udpListener) {
			defer workers.Done()
			if err := server.receive(runCtx, listener); err != nil {
				errorChannel <- err
			}
		}(listener)
	}

	workers.Add(1)
	go func() {
		defer workers.Done()
		server.cleanExpired(runCtx)
	}()

	var runError error
	select {
	case <-ctx.Done():
	case runError = <-errorChannel:
		cancel()
	}
	server.closeListeners()
	workers.Wait()
	return runError
}

func (server *Server) ReflectorPeers() []ReflectorPeers {
	now := time.Now()
	reflectorPeers := make([]ReflectorPeers, 0, len(server.relays))
	for _, relay := range server.relays {
		snapshot, expired := relay.registry.snapshot(now)
		server.metrics.SetActivePeers(relay.port, len(snapshot))
		server.logExpiredPeers(relay, expired, len(snapshot))
		peerCapacity := len(snapshot)
		if relay.registry.hasStaticHub() {
			peerCapacity++
		}
		peers := make([]Peer, 0, peerCapacity)
		for _, peer := range snapshot {
			peers = append(peers, Peer{
				Endpoint: peer.endpoint,
				LastSeen: peer.lastSeen,
				Hub:      relay.registry.isDynamicHubPeer(peer.endpoint),
			})
		}
		if relay.registry.hasStaticHub() {
			peers = append(peers, Peer{Endpoint: relay.registry.hubEndpoint, Hub: true})
		}
		sort.Slice(peers, func(first, second int) bool {
			return peers[first].Endpoint.String() < peers[second].Endpoint.String()
		})
		reflectorPeers = append(reflectorPeers, ReflectorPeers{ReflectorPort: relay.port, Peers: peers})
	}
	return reflectorPeers
}

func (server *Server) receive(ctx context.Context, listener *udpListener) error {
	buffer := make([]byte, maximumUDPPacketSize)
	for {
		read, sender, err := listener.conn.ReadFromUDPAddrPort(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			server.metrics.RecordListenerError(listener.port, listener.family)
			return fmt.Errorf("read from %s UDP port %d: %w", listener.family, listener.port, err)
		}

		server.metrics.RecordReceived(listener.port, listener.family, read)
		admission := listener.relay.registry.admit(sender, time.Now())
		server.metrics.SetActivePeers(listener.port, admission.active)
		server.logExpiredPeers(listener.relay, admission.expired, admission.active)
		if !admission.accepted {
			server.metrics.RecordDrop(listener.port, listener.family, "peer_capacity")
			continue
		}
		if admission.joined {
			hub := listener.relay.registry.isDynamicHubPeer(sender)
			server.logger.Info("Peer joined", "peer", sender.String(), "hub", hub, "port", listener.port, "active_peers", admission.active)
		}

		payload := buffer[:read]
		for _, recipient := range admission.recipients {
			family, connection := listener.relay.connectionFor(recipient)
			if _, err := connection.WriteToUDPAddrPort(payload, recipient); err != nil {
				server.metrics.RecordSendError(listener.port, family)
				continue
			}
			server.metrics.RecordForwarded(listener.port, family, read)
		}
	}
}

func (server *Server) cleanExpired(ctx context.Context) {
	interval := cleanupInterval(server.relays[0].registry.timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, relay := range server.relays {
				expired, active := relay.registry.expire(now)
				server.metrics.SetActivePeers(relay.port, active)
				server.logExpiredPeers(relay, expired, active)
			}
		}
	}
}

func (server *Server) logExpiredPeers(relay *portRelay, peers []netip.AddrPort, active int) {
	for _, peer := range peers {
		hub := relay.registry.isDynamicHubPeer(peer)
		server.logger.Info("Peer expired", "peer", peer.String(), "hub", hub, "port", relay.port, "active_peers", active)
	}
}

func (server *Server) closeListeners() {
	server.closeOnce.Do(func() {
		for _, listener := range server.listeners {
			server.metrics.SetListenerUp(listener.port, listener.family, false)
			if err := listener.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				server.logger.Warn("could not close UDP listener", "port", listener.port, "family", listener.family, "error", err)
			}
		}
	})
}

func (relay *portRelay) connectionFor(recipient netip.AddrPort) (string, *net.UDPConn) {
	if recipient.Addr().Is4() {
		return "ipv4", relay.udp4
	}
	return "ipv6", relay.udp6
}

func listenUDP(network string, address netip.Addr, port uint16) (*net.UDPConn, error) {
	return net.ListenUDP(network, &net.UDPAddr{IP: net.IP(address.AsSlice()), Port: int(port)})
}

func cleanupInterval(timeout time.Duration) time.Duration {
	interval := timeout / 2
	if interval > 30*time.Second {
		return 30 * time.Second
	}
	if interval < 10*time.Millisecond {
		return 10 * time.Millisecond
	}
	return interval
}
