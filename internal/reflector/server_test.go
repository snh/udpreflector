package reflector

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
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

func TestServerSubscribesPeerToAllPorts(t *testing.T) {
	portStart := availableConsecutiveDualStackPorts(t)
	settings := testSettings(portStart, portStart+1, 4)
	settings.SubscribeAllPorts = true
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	sourcePortStart := availableConsecutiveDualStackPortBlock(t, 2)
	first := listenClientOnPort(t, "udp4", "127.0.0.1", sourcePortStart)
	defer first.Close()
	firstNextPort := listenClientOnPort(t, "udp4", "127.0.0.1", sourcePortStart+1)
	defer firstNextPort.Close()
	second := listenClient(t, "udp4", "127.0.0.1")
	defer second.Close()

	writePacket(t, first, netip.MustParseAddrPort("127.0.0.1:"+portString(portStart)), []byte("register on first port"))
	waitForMappedPeerOnAllPorts(t, server, portStart, first.LocalAddr().(*net.UDPAddr).AddrPort())
	payload := []byte("send on second port")
	writePacket(t, second, netip.MustParseAddrPort("127.0.0.1:"+portString(portStart+1)), payload)
	if received := readPacket(t, firstNextPort); !bytes.Equal(received, payload) {
		t.Fatalf("mapped peer port received %q, want %q", received, payload)
	}
}

func TestServerMapsPeerSourcePortRangeToReflectorRange(t *testing.T) {
	portStart := availableConsecutiveDualStackPortBlock(t, 10)
	settings := testSettings(portStart, portStart+9, 4)
	settings.SubscribeAllPorts = true
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	t.Cleanup(server.closeListeners)

	sender := netip.MustParseAddrPort("192.0.2.1:5000")
	admissions := server.admitPeer(server.relays[0], sender, time.Now())

	if len(admissions) != 10 {
		t.Fatalf("admission count = %d, want 10", len(admissions))
	}
	for index, result := range admissions {
		wantEndpoint := netip.AddrPortFrom(sender.Addr(), uint16(5000+index))
		if !result.admission.accepted || result.endpoint != wantEndpoint {
			t.Fatalf("reflector port %d admission = %+v, endpoint %s; want accepted endpoint %s", result.relay.port, result.admission, result.endpoint, wantEndpoint)
		}
		snapshot, _ := result.relay.registry.snapshot(time.Now())
		if len(snapshot) != 1 || snapshot[0].endpoint != wantEndpoint {
			t.Fatalf("reflector port %d peers = %+v, want %s", result.relay.port, snapshot, wantEndpoint)
		}
	}
}

func TestServerCrossPortSubscriptionRefreshesEveryPort(t *testing.T) {
	portStart := availableConsecutiveDualStackPortBlock(t, 3)
	settings := testSettings(portStart, portStart+2, 4)
	settings.SubscribeAllPorts = true
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	t.Cleanup(server.closeListeners)

	sender := netip.MustParseAddrPort("192.0.2.1:1000")
	started := time.Unix(100, 0)
	server.admitPeer(server.relays[0], sender, started)
	refreshedAt := started.Add(30 * time.Second)
	admissions := server.admitPeer(server.relays[2], netip.MustParseAddrPort("192.0.2.1:1002"), refreshedAt)

	for index, result := range admissions {
		expected := netip.AddrPortFrom(sender.Addr(), sender.Port()+uint16(index))
		if !result.admission.accepted || result.admission.joined {
			t.Fatalf("port %d refresh = %+v, want accepted existing peer", result.relay.port, result.admission)
		}
		snapshot, expired := result.relay.registry.snapshot(started.Add(61 * time.Second))
		if len(expired) != 0 || len(snapshot) != 1 || snapshot[0].endpoint != expected || !snapshot[0].lastSeen.Equal(refreshedAt) {
			t.Fatalf("port %d snapshot = %+v, expired %v; want refreshed peer", result.relay.port, snapshot, expired)
		}
	}
}

func TestServerCrossPortSubscriptionExpiresFromEveryPort(t *testing.T) {
	portStart := availableConsecutiveDualStackPortBlock(t, 3)
	settings := testSettings(portStart, portStart+2, 4)
	settings.SubscribeAllPorts = true
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	t.Cleanup(server.closeListeners)

	sender := netip.MustParseAddrPort("192.0.2.1:1001")
	started := time.Unix(100, 0)
	server.admitPeer(server.relays[1], sender, started)

	for index, relay := range server.relays {
		expired, active := relay.registry.expire(started.Add(settings.PeerTimeout))
		assertRecipients(t, expired, netip.AddrPortFrom(sender.Addr(), uint16(1000+index)))
		if active != 0 {
			t.Fatalf("port %d active peers = %d, want 0", relay.port, active)
		}
	}
}

func TestServerCrossPortSubscriptionUsesExpiredCapacity(t *testing.T) {
	portStart := availableConsecutiveDualStackPorts(t)
	settings := testSettings(portStart, portStart+1, 1)
	settings.SubscribeAllPorts = true
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	t.Cleanup(server.closeListeners)

	started := time.Unix(100, 0)
	expiredPeer := netip.MustParseAddrPort("192.0.2.1:1000")
	server.relays[1].registry.admit(expiredPeer, started)
	sender := netip.MustParseAddrPort("192.0.2.2:2000")
	admissions := server.admitPeer(server.relays[0], sender, started.Add(settings.PeerTimeout))

	for index, result := range admissions {
		expected := netip.AddrPortFrom(sender.Addr(), sender.Port()+uint16(index))
		if !result.admission.accepted || !result.admission.joined || result.admission.active != 1 {
			t.Fatalf("port %d admission = %+v, want new peer after expiry", result.relay.port, result.admission)
		}
		if result.endpoint != expected {
			t.Fatalf("port %d endpoint = %s, want %s", result.relay.port, result.endpoint, expected)
		}
	}
	assertRecipients(t, admissions[1].admission.expired, expiredPeer)
}

func TestServerRejectsCrossPortSubscriptionAtomicallyAtCapacity(t *testing.T) {
	portStart := availableConsecutiveDualStackPorts(t)
	settings := testSettings(portStart, portStart+1, 1)
	settings.SubscribeAllPorts = true
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	t.Cleanup(server.closeListeners)

	now := time.Now()
	server.relays[1].registry.admit(netip.MustParseAddrPort("192.0.2.1:1000"), now)
	sender := netip.MustParseAddrPort("192.0.2.2:2000")
	admissions := server.admitPeer(server.relays[0], sender, now)

	for _, result := range admissions {
		if result.admission.accepted {
			t.Fatalf("port %d accepted peer despite full cross-port subscription", result.relay.port)
		}
		snapshot, _ := result.relay.registry.snapshot(now)
		for _, peer := range snapshot {
			if peer.endpoint == result.endpoint {
				t.Fatalf("port %d contains peer after atomic rejection", result.relay.port)
			}
		}
	}
}

func TestServerRejectsCrossPortSubscriptionMappingOutsideUDPRange(t *testing.T) {
	tests := []struct {
		name        string
		sourceIndex int
		senderPort  uint16
	}{
		{name: "underflow", sourceIndex: 2, senderPort: 2},
		{name: "overflow", sourceIndex: 0, senderPort: 65534},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			portStart := availableConsecutiveDualStackPortBlock(t, 3)
			settings := testSettings(portStart, portStart+2, 4)
			settings.SubscribeAllPorts = true
			server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
			if err != nil {
				t.Fatalf("NewServer(): %v", err)
			}
			t.Cleanup(server.closeListeners)

			sender := netip.AddrPortFrom(netip.MustParseAddr("192.0.2.1"), test.senderPort)
			admissions := server.admitPeer(server.relays[test.sourceIndex], sender, time.Now())

			if len(admissions) != 1 || admissions[0].admission.accepted {
				t.Fatalf("admissions = %+v, want one rejected source-port admission", admissions)
			}
			if admissions[0].admission.dropReason != "peer_port_mapping" {
				t.Fatalf("drop reason = %q, want peer_port_mapping", admissions[0].admission.dropReason)
			}
			for _, relay := range server.relays {
				snapshot, _ := relay.registry.snapshot(time.Now())
				if len(snapshot) != 0 {
					t.Fatalf("port %d peers = %+v, want none after invalid mapping", relay.port, snapshot)
				}
			}
		})
	}
}

func TestServerAtomicRejectionDoesNotRefreshExistingSubscription(t *testing.T) {
	portStart := availableConsecutiveDualStackPorts(t)
	settings := testSettings(portStart, portStart+1, 1)
	settings.SubscribeAllPorts = true
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	t.Cleanup(server.closeListeners)

	started := time.Unix(100, 0)
	sender := netip.MustParseAddrPort("192.0.2.1:1000")
	server.relays[0].registry.admit(sender, started)
	server.relays[1].registry.admit(netip.MustParseAddrPort("192.0.2.2:2000"), started)

	admissions := server.admitPeer(server.relays[0], sender, started.Add(30*time.Second))
	for _, result := range admissions {
		if result.admission.accepted {
			t.Fatalf("port %d accepted non-atomic refresh", result.relay.port)
		}
	}
	snapshot, _ := server.relays[0].registry.snapshot(started.Add(30 * time.Second))
	if len(snapshot) != 1 || !snapshot[0].lastSeen.Equal(started) {
		t.Fatalf("existing subscription was refreshed after rejection: %+v", snapshot)
	}
}

func TestServerClassifiesCrossSubscribedHubPerPort(t *testing.T) {
	portBlock := availableConsecutiveDualStackPortBlock(t, 4)
	settings := testSettings(portBlock, portBlock+1, 4)
	settings.HubEndpoint = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), portBlock+2)
	settings.SubscribeAllPorts = true
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	t.Cleanup(server.closeListeners)

	sender := settings.HubEndpoint
	admissions := server.admitPeer(server.relays[0], sender, time.Now())

	if !admissions[0].admission.accepted || !admissions[1].admission.accepted {
		t.Fatal("mapped hub endpoint was not subscribed to every port")
	}
	for _, result := range admissions {
		if !result.relay.registry.isHubPeer(result.endpoint) {
			t.Fatalf("mapped endpoint %s is not classified as the hub on port %d", result.endpoint, result.relay.port)
		}
	}
}

func TestServerClassifiesDynamicHubOnEverySubscribedPort(t *testing.T) {
	portStart := availableConsecutiveDualStackPorts(t)
	settings := testSettings(portStart, portStart+1, 4)
	settings.HubEndpoint = netip.AddrPortFrom(netip.MustParseAddr("192.0.2.100"), 0)
	settings.SubscribeAllPorts = true
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	t.Cleanup(server.closeListeners)

	spoke := netip.MustParseAddrPort("192.0.2.1:1000")
	hub := netip.MustParseAddrPort("192.0.2.100:30001")
	server.admitPeer(server.relays[0], spoke, time.Unix(100, 0))
	admissions := server.admitPeer(server.relays[1], hub, time.Unix(101, 0))

	for index, result := range admissions {
		if !result.relay.registry.isHubPeer(result.endpoint) {
			t.Fatalf("port %d did not classify dynamic hub", result.relay.port)
		}
		assertRecipients(t, result.admission.recipients, netip.AddrPortFrom(spoke.Addr(), spoke.Port()+uint16(index)))
	}
}

func TestServerCrossPortSubscriptionDoesNotEchoSender(t *testing.T) {
	portStart := availableConsecutiveDualStackPorts(t)
	settings := testSettings(portStart, portStart+1, 4)
	settings.SubscribeAllPorts = true
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	sourcePortStart := availableConsecutiveDualStackPortBlock(t, 2)
	firstPortClient := listenClientOnPort(t, "udp4", "127.0.0.1", sourcePortStart)
	defer firstPortClient.Close()
	secondPortClient := listenClientOnPort(t, "udp4", "127.0.0.1", sourcePortStart+1)
	defer secondPortClient.Close()
	writePacket(t, firstPortClient, netip.MustParseAddrPort("127.0.0.1:"+portString(portStart)), []byte("register"))
	waitForMappedPeerOnAllPorts(t, server, portStart, firstPortClient.LocalAddr().(*net.UDPAddr).AddrPort())

	writePacket(t, secondPortClient, netip.MustParseAddrPort("127.0.0.1:"+portString(portStart+1)), []byte("same peer range"))
	assertNoPacket(t, secondPortClient)
}

func TestServerCrossPortSubscriptionForwardsBetweenAddressFamilies(t *testing.T) {
	if !ipv6LoopbackAvailable() {
		t.Skip("IPv6 loopback is unavailable")
	}
	portStart := availableConsecutiveDualStackPorts(t)
	settings := testSettings(portStart, portStart+1, 4)
	settings.SubscribeAllPorts = true
	server, err := NewServer(settings, observability.NewMetrics(), discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	sourcePortStart := availableConsecutiveDualStackPortBlock(t, 2)
	ipv4Client := listenClientOnPort(t, "udp4", "127.0.0.1", sourcePortStart)
	defer ipv4Client.Close()
	ipv4NextPort := listenClientOnPort(t, "udp4", "127.0.0.1", sourcePortStart+1)
	defer ipv4NextPort.Close()
	ipv6Client := listenClient(t, "udp6", "::1")
	defer ipv6Client.Close()
	writePacket(t, ipv4Client, netip.MustParseAddrPort("127.0.0.1:"+portString(portStart)), []byte("register IPv4"))
	waitForMappedPeerOnAllPorts(t, server, portStart, ipv4Client.LocalAddr().(*net.UDPAddr).AddrPort())

	payload := []byte("from IPv6 on another port")
	writePacket(t, ipv6Client, netip.MustParseAddrPort("[::1]:"+portString(portStart+1)), payload)
	if received := readPacket(t, ipv4NextPort); !bytes.Equal(received, payload) {
		t.Fatalf("mapped IPv4 peer port received %q, want %q", received, payload)
	}
}

func TestServerCrossPortSubscriptionUpdatesMetricsForEveryPort(t *testing.T) {
	portStart := availableConsecutiveDualStackPorts(t)
	settings := testSettings(portStart, portStart+1, 4)
	settings.SubscribeAllPorts = true
	metrics := observability.NewMetrics()
	server, err := NewServer(settings, metrics, discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	sourcePortStart := availableConsecutiveDualStackPortBlock(t, 2)
	client := listenClientOnPort(t, "udp4", "127.0.0.1", sourcePortStart)
	defer client.Close()
	writePacket(t, client, netip.MustParseAddrPort("127.0.0.1:"+portString(portStart)), []byte("register"))
	waitForMappedPeerOnAllPorts(t, server, portStart, client.LocalAddr().(*net.UDPAddr).AddrPort())

	request := httptest.NewRequest("GET", "/metrics", nil)
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, request)
	for port := portStart; port <= portStart+1; port++ {
		want := `udp_reflector_active_peers{port="` + portString(port) + `"} 1`
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("metrics response does not contain %q", want)
		}
	}
}

func TestServerCrossPortSubscriptionLogsJoinForEveryPort(t *testing.T) {
	portStart := availableConsecutiveDualStackPorts(t)
	settings := testSettings(portStart, portStart+1, 4)
	settings.SubscribeAllPorts = true
	logs := make(chan logEntry, 2)
	server, err := NewServer(settings, observability.NewMetrics(), slog.New(logHandler{logs: logs}))
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	cancel, result := runTestServer(server)
	defer stopTestServer(t, cancel, result)

	sourcePortStart := availableConsecutiveDualStackPortBlock(t, 2)
	client := listenClientOnPort(t, "udp4", "127.0.0.1", sourcePortStart)
	defer client.Close()
	writePacket(t, client, netip.MustParseAddrPort("127.0.0.1:"+portString(portStart)), []byte("register"))

	for port := portStart; port <= portStart+1; port++ {
		entry := waitForLog(t, logs)
		expectedPeer := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), sourcePortStart+(port-portStart))
		if entry.message != "Peer joined" || entry.attributes["peer"] != expectedPeer.String() || entry.attributes["port"] != portString(port) || entry.attributes["active_peers"] != "1" {
			t.Fatalf("join log = %+v, want port %d with one active peer", entry, port)
		}
	}
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
	if !reflectorPeers[0].HubAndSpoke || reflectorPeers[1].HubAndSpoke {
		t.Fatalf("hub-and-spoke modes = (%t, %t), want (true, false)", reflectorPeers[0].HubAndSpoke, reflectorPeers[1].HubAndSpoke)
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
	lastSeen := []time.Time{time.Unix(100, 0), time.Unix(200, 0)}
	for index, relay := range server.relays {
		hubEndpoint := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), hubPortStart+uint16(index))
		relay.registry.admit(hubEndpoint, lastSeen[index])
	}

	reflectorPeers := server.ReflectorPeers()

	if len(reflectorPeers) != 2 {
		t.Fatalf("ReflectorPeers() returned %d entries, want 2", len(reflectorPeers))
	}
	for index, reflector := range reflectorPeers {
		if !reflector.HubAndSpoke {
			t.Fatalf("reflector %d hub-and-spoke mode = false, want true", reflector.ReflectorPort)
		}
		if len(reflector.Peers) != 1 {
			t.Fatalf("reflector %d peers = %+v, want one static hub", reflector.ReflectorPort, reflector.Peers)
		}
		hub := reflector.Peers[0]
		wantEndpoint := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), hubPortStart+uint16(index))
		if hub.Endpoint != wantEndpoint || !hub.Hub || !hub.LastSeen.Equal(lastSeen[index]) {
			t.Fatalf("reflector %d peer = %+v, want static hub %s last seen at %s", reflector.ReflectorPort, hub, wantEndpoint, lastSeen[index])
		}
	}
}

func TestServerCountsStaticHubAsActivePeerAtStartup(t *testing.T) {
	portBlock := availableConsecutiveDualStackPortBlock(t, 2)
	settings := testSettings(portBlock, portBlock, 1)
	settings.HubEndpoint = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), portBlock+1)
	metrics := observability.NewMetrics()
	server, err := NewServer(settings, metrics, discardLogger())
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	t.Cleanup(server.closeListeners)

	request := httptest.NewRequest("GET", "/metrics", nil)
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, request)
	want := `udp_reflector_active_peers{port="` + portString(portBlock) + `"} 1`
	if !strings.Contains(response.Body.String(), want) {
		t.Fatalf("metrics response does not contain %q", want)
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
	settings := testSettings(port, port, 3)
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
	settings := testSettings(port, port, 2)
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

func TestValidateSettingsRejectsInvalidSettings(t *testing.T) {
	valid := testSettings(40000, 40001, 4)
	tests := []struct {
		name   string
		mutate func(*Settings)
	}{
		{name: "zero start port", mutate: func(settings *Settings) {
			settings.PortRange.Start = 0
		}},
		{name: "reversed port range", mutate: func(settings *Settings) {
			settings.PortRange = PortRange{Start: 40001, End: 40000}
		}},
		{name: "unspecified hub", mutate: func(settings *Settings) {
			settings.HubEndpoint = netip.MustParseAddrPort("0.0.0.0:30000")
		}},
		{name: "zero timeout", mutate: func(settings *Settings) {
			settings.PeerTimeout = 0
		}},
		{name: "zero peer capacity", mutate: func(settings *Settings) {
			settings.MaxPeersPerPort = 0
		}},
		{name: "IPv6 UDP4 address", mutate: func(settings *Settings) {
			settings.UDP4Address = netip.MustParseAddr("::1")
		}},
		{name: "IPv4 UDP6 address", mutate: func(settings *Settings) {
			settings.UDP6Address = netip.MustParseAddr("127.0.0.1")
		}},
		{name: "mapped IPv4 UDP6 address", mutate: func(settings *Settings) {
			settings.UDP6Address = netip.MustParseAddr("::ffff:127.0.0.1")
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := valid
			test.mutate(&settings)
			if err := validateSettings(settings); err == nil {
				t.Fatal("validateSettings() returned nil error")
			}
		})
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

func TestServerFailsWhenIPv4PortIsOccupied(t *testing.T) {
	port := availableDualStackPort(t)
	occupied, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: int(port)})
	if err != nil {
		t.Fatalf("occupy IPv4 port: %v", err)
	}
	defer occupied.Close()

	if _, err := NewServer(testSettings(port, port, 4), observability.NewMetrics(), discardLogger()); err == nil {
		t.Fatal("NewServer() succeeded despite an occupied IPv4 port")
	}
}

func TestServerReturnsUnexpectedListenerFailure(t *testing.T) {
	port := availableDualStackPort(t)
	server := newTestServer(t, port, port, 4)
	_, result := runTestServer(server)

	if err := server.relays[0].udp4.Close(); err != nil {
		t.Fatalf("close IPv4 listener: %v", err)
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "read from ipv4 UDP port") {
			t.Fatalf("Run() error = %v, want IPv4 listener failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after listener failure")
	}
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

func TestCleanupIntervalBoundsAndScales(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "caps long timeout", timeout: 2 * time.Minute, want: 30 * time.Second},
		{name: "halves ordinary timeout", timeout: 10 * time.Second, want: 5 * time.Second},
		{name: "floors short timeout", timeout: time.Millisecond, want: 10 * time.Millisecond},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := cleanupInterval(test.timeout); got != test.want {
				t.Fatalf("cleanupInterval(%s) = %s, want %s", test.timeout, got, test.want)
			}
		})
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

func waitForMappedPeerOnAllPorts(t *testing.T, server *Server, sourcePort uint16, endpoint netip.AddrPort) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		reflectors := server.ReflectorPeers()
		foundOnEveryPort := len(reflectors) > 0
		for _, reflector := range reflectors {
			mappedEndpoint := netip.AddrPortFrom(
				endpoint.Addr(),
				uint16(int(endpoint.Port())+int(reflector.ReflectorPort)-int(sourcePort)),
			)
			found := false
			for _, peer := range reflector.Peers {
				if peer.Endpoint == mappedEndpoint {
					found = true
					break
				}
			}
			if !found {
				foundOnEveryPort = false
				break
			}
		}
		if foundOnEveryPort {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("peer range based at %s was not subscribed to every port", endpoint)
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
