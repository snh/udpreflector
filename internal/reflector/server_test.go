package reflector

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"udpreflector/internal/observability"
)

func TestServerForwardsPayloadToOtherIPv4Peers(t *testing.T) {
	port := availableDualStackPort(t)
	server := newTestServer(t, port, port, 4)
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	first := listenClient(t, "udp4", "127.0.0.1")
	defer first.Close()
	second := listenClient(t, "udp4", "127.0.0.1")
	defer second.Close()
	destination := netip.MustParseAddrPort("127.0.0.1:" + portString(port))

	writePacket(t, first, destination, []byte("register first"))
	payload := []byte{0x00, 0x01, 0xfe, 0xff, 0x42}
	writePacket(t, second, destination, payload)
	if received := readPacket(t, first); !bytes.Equal(received, payload) {
		t.Fatalf("received payload = %v, want %v", received, payload)
	}
	assertNoPacket(t, second)
}

func TestServerFansOutToMultipleIPv4Peers(t *testing.T) {
	port := availableDualStackPort(t)
	server := newTestServer(t, port, port, 4)
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	first := listenClient(t, "udp4", "127.0.0.1")
	defer first.Close()
	second := listenClient(t, "udp4", "127.0.0.1")
	defer second.Close()
	third := listenClient(t, "udp4", "127.0.0.1")
	defer third.Close()
	destination := netip.MustParseAddrPort("127.0.0.1:" + portString(port))

	writePacket(t, first, destination, []byte("register first"))
	writePacket(t, second, destination, []byte("register second"))
	readPacket(t, first)

	payload := []byte("fan out")
	writePacket(t, third, destination, payload)
	if received := readPacket(t, first); !bytes.Equal(received, payload) {
		t.Fatalf("first peer received %q, want %q", received, payload)
	}
	if received := readPacket(t, second); !bytes.Equal(received, payload) {
		t.Fatalf("second peer received %q, want %q", received, payload)
	}
	assertNoPacket(t, third)
}

func TestServerForwardsBetweenAddressFamilies(t *testing.T) {
	if !ipv6LoopbackAvailable() {
		t.Skip("IPv6 loopback is unavailable")
	}
	port := availableDualStackPort(t)
	server := newTestServer(t, port, port, 4)
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	ipv4Client := listenClient(t, "udp4", "127.0.0.1")
	defer ipv4Client.Close()
	ipv6Client := listenClient(t, "udp6", "::1")
	defer ipv6Client.Close()
	ipv4Destination := netip.MustParseAddrPort("127.0.0.1:" + portString(port))
	ipv6Destination := netip.MustParseAddrPort("[::1]:" + portString(port))

	fromIPv4 := []byte("from IPv4")
	writePacket(t, ipv4Client, ipv4Destination, fromIPv4)
	fromIPv6 := []byte("from IPv6")
	received, err := exchangeUntilReceived(ipv6Client, ipv6Destination, ipv4Client, fromIPv6)
	if err != nil {
		t.Fatalf("wait for IPv4 peer registration: %v", err)
	}
	if !bytes.Equal(received, fromIPv6) {
		t.Fatalf("IPv4 client received %q, want %q", received, fromIPv6)
	}

	writePacket(t, ipv4Client, ipv4Destination, fromIPv4)
	if received := readPacket(t, ipv6Client); !bytes.Equal(received, fromIPv4) {
		t.Fatalf("IPv6 client received %q, want %q", received, fromIPv4)
	}
}

func TestServerDoesNotForwardRejectedPeerPacket(t *testing.T) {
	port := availableDualStackPort(t)
	server := newTestServer(t, port, port, 1)
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	registered := listenClient(t, "udp4", "127.0.0.1")
	defer registered.Close()
	rejected := listenClient(t, "udp4", "127.0.0.1")
	defer rejected.Close()
	destination := netip.MustParseAddrPort("127.0.0.1:" + portString(port))

	writePacket(t, registered, destination, []byte("register"))
	writePacket(t, rejected, destination, []byte("must be dropped"))
	assertNoPacket(t, registered)
	assertNoPacket(t, rejected)
}

func TestServerKeepsPortsIsolated(t *testing.T) {
	portStart := availableConsecutiveDualStackPorts(t)
	portEnd := portStart + 1
	server := newTestServer(t, portStart, portEnd, 4)
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	firstPortClient := listenClient(t, "udp4", "127.0.0.1")
	defer firstPortClient.Close()
	secondPortClient := listenClient(t, "udp4", "127.0.0.1")
	defer secondPortClient.Close()

	writePacket(t, firstPortClient, netip.MustParseAddrPort("127.0.0.1:"+portString(portStart)), []byte("register first port"))
	writePacket(t, secondPortClient, netip.MustParseAddrPort("127.0.0.1:"+portString(portEnd)), []byte("second port packet"))
	assertNoPacket(t, firstPortClient)
}

func TestServerListsReflectorPeersByPortAndEndpoint(t *testing.T) {
	portStart := availableConsecutiveDualStackPorts(t)
	server := newTestServer(t, portStart, portStart+1, 4)
	t.Cleanup(server.closeListeners)
	server.relays[0].registry.hubEndpoint = netip.MustParseAddrPort("192.0.2.2:0")
	now := time.Now()
	server.relays[0].registry.admit(netip.MustParseAddrPort("192.0.2.2:2000"), now)
	server.relays[0].registry.admit(netip.MustParseAddrPort("192.0.2.1:1000"), now)
	server.relays[1].registry.admit(netip.MustParseAddrPort("[2001:db8::1]:3000"), now)

	reflectorPeers := server.ReflectorPeers()

	if len(reflectorPeers) != 2 {
		t.Fatalf("ReflectorPeers() returned %d entries, want 2", len(reflectorPeers))
	}
	if reflectorPeers[0].ReflectorPort != portStart || reflectorPeers[1].ReflectorPort != portStart+1 {
		t.Fatalf("reflector ports = (%d, %d), want (%d, %d)", reflectorPeers[0].ReflectorPort, reflectorPeers[1].ReflectorPort, portStart, portStart+1)
	}
	if len(reflectorPeers[0].Peers) != 2 || reflectorPeers[0].Peers[0].Endpoint.String() != "192.0.2.1:1000" || reflectorPeers[0].Peers[0].Hub {
		t.Fatalf("first reflector peers = %+v, want sorted spoke then hub", reflectorPeers[0].Peers)
	}
	if reflectorPeers[0].Peers[1].Endpoint.String() != "192.0.2.2:2000" || !reflectorPeers[0].Peers[1].Hub {
		t.Fatalf("second peer = %+v, want dynamic hub", reflectorPeers[0].Peers[1])
	}
	if len(reflectorPeers[1].Peers) != 1 || reflectorPeers[1].Peers[0].Endpoint.String() != "[2001:db8::1]:3000" || reflectorPeers[1].Peers[0].Hub {
		t.Fatalf("second reflector peers = %+v, want one spoke", reflectorPeers[1].Peers)
	}
}

func TestServerListsMappedStaticHubForEachPort(t *testing.T) {
	portBlock := availableConsecutiveDualStackPortBlock(t, 4)
	portStart := portBlock
	hubPortStart := portBlock + 2
	settings := testSettings(portStart, portStart+1, 4)
	settings.HubEndpoint = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), hubPortStart)
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	t.Cleanup(server.closeListeners)

	reflectorPeers := server.ReflectorPeers()

	if len(reflectorPeers) != 2 {
		t.Fatalf("ReflectorPeers() returned %d entries, want 2", len(reflectorPeers))
	}
	for index, reflector := range reflectorPeers {
		if len(reflector.Peers) != 1 {
			t.Fatalf("reflector %d peers = %+v, want one static hub", reflector.ReflectorPort, reflector.Peers)
		}
		hub := reflector.Peers[0]
		wantEndpoint := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), hubPortStart+uint16(index))
		if hub.Endpoint != wantEndpoint || !hub.Hub || !hub.LastSeen.IsZero() {
			t.Fatalf("reflector %d peer = %+v, want static hub %s with no last-seen time", reflector.ReflectorPort, hub, wantEndpoint)
		}
	}
}

func TestServerMapsConsecutivePortsToHubEndpoints(t *testing.T) {
	portBlock := availableConsecutiveDualStackPortBlock(t, 4)
	portStart := portBlock
	hubPortStart := portBlock + 2
	settings := testSettings(portStart, portStart+1, 4)
	settings.HubEndpoint = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), hubPortStart)
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	firstHub := listenClientOnPort(t, "udp4", "127.0.0.1", hubPortStart)
	defer firstHub.Close()
	secondHub := listenClientOnPort(t, "udp4", "127.0.0.1", hubPortStart+1)
	defer secondHub.Close()
	firstSpoke := listenClient(t, "udp4", "127.0.0.1")
	defer firstSpoke.Close()
	secondSpoke := listenClient(t, "udp4", "127.0.0.1")
	defer secondSpoke.Close()
	firstDestination := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), portStart)
	secondDestination := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), portStart+1)

	firstPayload := []byte("first mapped port")
	writePacket(t, firstSpoke, firstDestination, firstPayload)
	if received := readPacket(t, firstHub); !bytes.Equal(received, firstPayload) {
		t.Fatalf("first hub received %q, want %q", received, firstPayload)
	}
	assertNoPacket(t, secondHub)

	secondPayload := []byte("second mapped port")
	writePacket(t, secondSpoke, secondDestination, secondPayload)
	if received := readPacket(t, secondHub); !bytes.Equal(received, secondPayload) {
		t.Fatalf("second hub received %q, want %q", received, secondPayload)
	}
	assertNoPacket(t, firstHub)

	fromFirstHub := []byte("from first hub")
	writePacket(t, firstHub, firstDestination, fromFirstHub)
	if received := readPacket(t, firstSpoke); !bytes.Equal(received, fromFirstHub) {
		t.Fatalf("first spoke received %q, want %q", received, fromFirstHub)
	}
	assertNoPacket(t, secondSpoke)

	fromSecondHub := []byte("from second hub")
	writePacket(t, secondHub, secondDestination, fromSecondHub)
	if received := readPacket(t, secondSpoke); !bytes.Equal(received, fromSecondHub) {
		t.Fatalf("second spoke received %q, want %q", received, fromSecondHub)
	}
	assertNoPacket(t, firstSpoke)
}

func TestServerHubFansOutToAllSpokes(t *testing.T) {
	portBlock := availableConsecutiveDualStackPortBlock(t, 2)
	port := portBlock
	hubPort := portBlock + 1
	settings := testSettings(port, port, 2)
	settings.HubEndpoint = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), hubPort)
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	hub := listenClientOnPort(t, "udp4", "127.0.0.1", hubPort)
	defer hub.Close()
	first := listenClient(t, "udp4", "127.0.0.1")
	defer first.Close()
	second := listenClient(t, "udp4", "127.0.0.1")
	defer second.Close()
	destination := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port)

	writePacket(t, first, destination, []byte("register first"))
	readPacket(t, hub)
	writePacket(t, second, destination, []byte("register second"))
	readPacket(t, hub)

	payload := []byte("hub fan out")
	writePacket(t, hub, destination, payload)
	if received := readPacket(t, first); !bytes.Equal(received, payload) {
		t.Fatalf("first spoke received %q, want %q", received, payload)
	}
	if received := readPacket(t, second); !bytes.Equal(received, payload) {
		t.Fatalf("second spoke received %q, want %q", received, payload)
	}
}

func TestServerForwardsBetweenIPv4SpokeAndIPv6Hub(t *testing.T) {
	if !ipv6LoopbackAvailable() {
		t.Skip("IPv6 loopback is unavailable")
	}
	portBlock := availableConsecutiveDualStackPortBlock(t, 2)
	port := portBlock
	hubPort := portBlock + 1
	settings := testSettings(port, port, 1)
	settings.HubEndpoint = netip.AddrPortFrom(netip.MustParseAddr("::1"), hubPort)
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	hub := listenClientOnPort(t, "udp6", "::1", hubPort)
	defer hub.Close()
	spoke := listenClient(t, "udp4", "127.0.0.1")
	defer spoke.Close()
	ipv4Destination := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port)
	ipv6Destination := netip.AddrPortFrom(netip.MustParseAddr("::1"), port)

	fromSpoke := []byte("from IPv4 spoke")
	writePacket(t, spoke, ipv4Destination, fromSpoke)
	if received := readPacket(t, hub); !bytes.Equal(received, fromSpoke) {
		t.Fatalf("IPv6 hub received %q, want %q", received, fromSpoke)
	}

	fromHub := []byte("from IPv6 hub")
	writePacket(t, hub, ipv6Destination, fromHub)
	if received := readPacket(t, spoke); !bytes.Equal(received, fromHub) {
		t.Fatalf("IPv4 spoke received %q, want %q", received, fromHub)
	}
}

func TestServerLearnsHubEndpointWhenPortIsOmitted(t *testing.T) {
	if !ipv6LoopbackAvailable() {
		t.Skip("IPv6 loopback is unavailable")
	}
	port := availableDualStackPort(t)
	settings := testSettings(port, port, 2)
	settings.HubEndpoint = netip.AddrPortFrom(netip.MustParseAddr("::1"), 0)
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	hub := listenClient(t, "udp6", "::1")
	defer hub.Close()
	spoke := listenClient(t, "udp4", "127.0.0.1")
	defer spoke.Close()
	ipv4Destination := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port)
	ipv6Destination := netip.AddrPortFrom(netip.MustParseAddr("::1"), port)

	writePacket(t, spoke, ipv4Destination, []byte("before hub registration"))
	assertNoPacket(t, hub)

	fromHub := []byte("register dynamic hub")
	writePacket(t, hub, ipv6Destination, fromHub)
	if received := readPacket(t, spoke); !bytes.Equal(received, fromHub) {
		t.Fatalf("IPv4 spoke received %q, want %q", received, fromHub)
	}

	fromSpoke := []byte("to dynamic hub")
	writePacket(t, spoke, ipv4Destination, fromSpoke)
	if received := readPacket(t, hub); !bytes.Equal(received, fromSpoke) {
		t.Fatalf("dynamic hub received %q, want %q", received, fromSpoke)
	}
}

func TestValidateSettingsRejectsHubPortMappingOverflow(t *testing.T) {
	settings := testSettings(40000, 40001, 4)
	settings.HubEndpoint = netip.MustParseAddrPort("192.0.2.1:65535")

	if err := validateSettings(settings); err == nil {
		t.Fatal("validateSettings() accepted a hub mapping above port 65535")
	}
}

func TestServerRollsBackEarlierBindOnFailure(t *testing.T) {
	port := availableDualStackPort(t)
	occupied, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.ParseIP("::1"), Port: int(port)})
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	defer occupied.Close()

	_, err = NewServer(testSettings(port, port, 4), observability.NewMetrics(), discardLogger())
	if err == nil {
		t.Fatal("NewServer() succeeded despite an occupied IPv6 port")
	}

	rebound, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: int(port)})
	if err != nil {
		t.Fatalf("IPv4 listener was not rolled back: %v", err)
	}
	rebound.Close()
}

func TestServerStopsGracefullyWhenCancelled(t *testing.T) {
	port := availableDualStackPort(t)
	server := newTestServer(t, port, port, 4)
	cancel, result := runTestServer(server)
	cancel()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() returned an error during cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func TestServerLogsPeerJoiningAndExpiring(t *testing.T) {
	port := availableDualStackPort(t)
	settings := testSettings(port, port, 4)
	settings.PeerTimeout = 20 * time.Millisecond
	logs := make(chan logEntry, 2)
	server, err := NewServer(settings, observability.NewMetrics(), slog.New(logHandler{logs: logs}))
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	client := listenClient(t, "udp4", "127.0.0.1")
	defer client.Close()
	writePacket(t, client, netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port), []byte("join"))

	joined := waitForLog(t, logs)
	if joined.message != "Peer joined" {
		t.Fatalf("first log message = %q, want peer joined", joined.message)
	}
	if joined.attributes["peer"] != client.LocalAddr().String() || joined.attributes["port"] != portString(port) || joined.attributes["active_peers"] != "1" {
		t.Fatalf("peer joined attributes = %v", joined.attributes)
	}
	if joined.attributes["hub"] != "false" {
		t.Fatalf("peer joined hub attribute = %q, want false", joined.attributes["hub"])
	}

	expired := waitForLog(t, logs)
	if expired.message != "Peer expired" {
		t.Fatalf("second log message = %q, want peer expired", expired.message)
	}
	if expired.attributes["peer"] != client.LocalAddr().String() || expired.attributes["port"] != portString(port) || expired.attributes["active_peers"] != "0" {
		t.Fatalf("peer expired attributes = %v", expired.attributes)
	}
	if expired.attributes["hub"] != "false" {
		t.Fatalf("peer expired hub attribute = %q, want false", expired.attributes["hub"])
	}
}

func TestServerLogsHubJoiningAndExpiring(t *testing.T) {
	port := availableDualStackPort(t)
	settings := testSettings(port, port, 1)
	settings.HubEndpoint = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 0)
	settings.PeerTimeout = 20 * time.Millisecond
	logs := make(chan logEntry, 2)
	server, err := NewServer(settings, observability.NewMetrics(), slog.New(logHandler{logs: logs}))
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	hub := listenClient(t, "udp4", "127.0.0.1")
	defer hub.Close()
	writePacket(t, hub, netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port), []byte("join"))

	joined := waitForLog(t, logs)
	if joined.message != "Peer joined" {
		t.Fatalf("first log message = %q, want peer joined", joined.message)
	}
	if joined.attributes["peer"] != hub.LocalAddr().String() || joined.attributes["hub"] != "true" || joined.attributes["port"] != portString(port) || joined.attributes["active_peers"] != "1" {
		t.Fatalf("hub joined attributes = %v", joined.attributes)
	}

	expired := waitForLog(t, logs)
	if expired.message != "Peer expired" {
		t.Fatalf("second log message = %q, want peer expired", expired.message)
	}
	if expired.attributes["peer"] != hub.LocalAddr().String() || expired.attributes["hub"] != "true" || expired.attributes["port"] != portString(port) || expired.attributes["active_peers"] != "0" {
		t.Fatalf("hub expired attributes = %v", expired.attributes)
	}
}

type logEntry struct {
	message    string
	attributes map[string]string
}

type logHandler struct {
	logs chan<- logEntry
}

func (handler logHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (handler logHandler) Handle(_ context.Context, record slog.Record) error {
	entry := logEntry{message: record.Message, attributes: make(map[string]string)}
	record.Attrs(func(attribute slog.Attr) bool {
		entry.attributes[attribute.Key] = attribute.Value.String()
		return true
	})
	handler.logs <- entry
	return nil
}

func (handler logHandler) WithAttrs([]slog.Attr) slog.Handler {
	return handler
}

func (handler logHandler) WithGroup(string) slog.Handler {
	return handler
}

func waitForLog(t *testing.T, logs <-chan logEntry) logEntry {
	t.Helper()
	select {
	case entry := <-logs:
		return entry
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log entry")
		return logEntry{}
	}
}

func newTestServer(t *testing.T, portStart uint16, portEnd uint16, maximumPeers int) *Server {
	t.Helper()
	server, err := NewServer(testSettings(portStart, portEnd, maximumPeers), observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	return server
}

func testSettings(portStart uint16, portEnd uint16, maximumPeers int) Settings {
	return Settings{
		PortRange:       PortRange{Start: portStart, End: portEnd},
		PeerTimeout:     time.Minute,
		MaxPeersPerPort: maximumPeers,
		UDP4Address:     netip.MustParseAddr("127.0.0.1"),
		UDP6Address:     netip.MustParseAddr("::1"),
	}
}

func runTestServer(server *Server) (context.CancelFunc, <-chan error) {
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- server.Run(ctx)
	}()
	return cancel, result
}

func stopTestServer(t *testing.T, cancel context.CancelFunc, result <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("Run() returned an error during shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("Run() did not stop during shutdown")
	}
}

func availableDualStackPort(t *testing.T) uint16 {
	t.Helper()
	for attempts := 0; attempts < 20; attempts++ {
		candidate, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			t.Fatalf("find available UDP port: %v", err)
		}
		port := uint16(candidate.LocalAddr().(*net.UDPAddr).Port)
		candidate.Close()
		probe4, err4 := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: int(port)})
		if err4 != nil {
			continue
		}
		probe6, err6 := net.ListenUDP("udp6", &net.UDPAddr{IP: net.ParseIP("::1"), Port: int(port)})
		probe4.Close()
		if err6 == nil {
			probe6.Close()
			return port
		}
	}
	t.Fatal("could not find an available dual-stack UDP port")
	return 0
}

func availableConsecutiveDualStackPorts(t *testing.T) uint16 {
	return availableConsecutiveDualStackPortBlock(t, 2)
}

func availableConsecutiveDualStackPortBlock(t *testing.T, count uint16) uint16 {
	t.Helper()
	for candidate := uint16(30000); uint32(candidate)+uint32(count) <= 60000; candidate += count {
		connections := make([]*net.UDPConn, 0, int(count)*2)
		available := true
		for offset := uint16(0); offset < count; offset++ {
			port := candidate + offset
			for _, listen := range []struct {
				network string
				address string
			}{{"udp4", "127.0.0.1"}, {"udp6", "::1"}} {
				connection, err := net.ListenUDP(listen.network, &net.UDPAddr{IP: net.ParseIP(listen.address), Port: int(port)})
				if err != nil {
					available = false
					break
				}
				connections = append(connections, connection)
			}
			if !available {
				break
			}
		}
		for _, connection := range connections {
			connection.Close()
		}
		if available {
			return candidate
		}
	}
	t.Fatalf("could not find %d consecutive dual-stack UDP ports", count)
	return 0
}

func listenClient(t *testing.T, network string, address string) *net.UDPConn {
	return listenClientOnPort(t, network, address, 0)
}

func listenClientOnPort(t *testing.T, network string, address string, port uint16) *net.UDPConn {
	t.Helper()
	connection, err := net.ListenUDP(network, &net.UDPAddr{IP: net.ParseIP(address), Port: int(port)})
	if err != nil {
		t.Fatalf("listen with %s client: %v", network, err)
	}
	return connection
}

func writePacket(t *testing.T, connection *net.UDPConn, destination netip.AddrPort, payload []byte) {
	t.Helper()
	if _, err := connection.WriteToUDPAddrPort(payload, destination); err != nil {
		t.Fatalf("write UDP packet: %v", err)
	}
}

func readPacket(t *testing.T, connection *net.UDPConn) []byte {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buffer := make([]byte, maximumUDPPacketSize)
	read, _, err := connection.ReadFromUDPAddrPort(buffer)
	if err != nil {
		t.Fatalf("read UDP packet: %v", err)
	}
	return buffer[:read]
}

func exchangeUntilReceived(sender *net.UDPConn, destination netip.AddrPort, recipient *net.UDPConn, payload []byte) ([]byte, error) {
	buffer := make([]byte, maximumUDPPacketSize)
	for attempts := 0; attempts < 10; attempts++ {
		if _, err := sender.WriteToUDPAddrPort(payload, destination); err != nil {
			return nil, err
		}
		if err := recipient.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			return nil, err
		}
		read, _, err := recipient.ReadFromUDPAddrPort(buffer)
		if err == nil {
			return buffer[:read], nil
		}
		if netError, ok := err.(net.Error); !ok || !netError.Timeout() {
			return nil, err
		}
	}
	return nil, context.DeadlineExceeded
}

func assertNoPacket(t *testing.T, connection *net.UDPConn) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buffer := make([]byte, 1)
	if _, _, err := connection.ReadFromUDPAddrPort(buffer); err == nil {
		t.Fatal("sender unexpectedly received its own packet")
	} else if netError, ok := err.(net.Error); !ok || !netError.Timeout() {
		t.Fatalf("checking for sender echo: %v", err)
	}
}

func ipv6LoopbackAvailable() bool {
	connection, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.ParseIP("::1")})
	if err != nil {
		return false
	}
	connection.Close()
	return true
}

func portString(port uint16) string {
	return strconv.FormatUint(uint64(port), 10)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
