package reflector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"udpreflector/internal/observability"
)

const maximumUDPPacketSize = 65535

type Settings struct {
	PortStart       uint16
	PortEnd         uint16
	PeerTimeout     time.Duration
	MaxPeersPerPort int
	UDP4Address     netip.Addr
	UDP6Address     netip.Addr
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
	for port := settings.PortStart; ; port++ {
		relay := &portRelay{
			port:     port,
			registry: newPeerRegistry(settings.PeerTimeout, settings.MaxPeersPerPort),
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

		if port == settings.PortEnd {
			break
		}
	}
	return server, nil
}

func validateSettings(settings Settings) error {
	if settings.PortStart == 0 || settings.PortEnd < settings.PortStart {
		return errors.New("invalid UDP port range")
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
		if !admission.accepted {
			server.metrics.RecordDrop(listener.port, listener.family, "peer_capacity")
			continue
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
				_, active := relay.registry.expire(now)
				server.metrics.SetActivePeers(relay.port, active)
			}
		}
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
