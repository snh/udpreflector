package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"udpreflector/internal/config"
)

func TestRunServesPeerAPIAndMetricsOnOneListener(t *testing.T) {
	configuration := testConfiguration(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, configuration, discardLogger())
	}()

	metricsURL := "http://" + configuration.HTTPAddress + "/metrics"
	response := waitForHTTP(t, metricsURL)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", response.StatusCode)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("read metrics response: %v", err)
	}
	response.Body.Close()

	healthURL := "http://" + configuration.HTTPAddress + "/healthz"
	response = waitForHTTP(t, healthURL)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", response.StatusCode)
	}
	healthBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read health response: %v", err)
	}
	if string(healthBody) != "ok\n" {
		t.Fatalf("health body = %q, want %q", healthBody, "ok\n")
	}

	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen as peer: %v", err)
	}
	defer peer.Close()
	destination := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), configuration.PortRange.Start)
	if _, err := peer.WriteToUDPAddrPort([]byte("register peer"), destination); err != nil {
		t.Fatalf("register peer: %v", err)
	}

	peersURL := "http://" + configuration.HTTPAddress + "/peers"
	reflectorPeers := waitForReflectorPeers(t, peersURL, 1)
	if reflectorPeers[0].ReflectorPort != configuration.PortRange.Start {
		t.Fatalf("peer reflector port = %d, want %d", reflectorPeers[0].ReflectorPort, configuration.PortRange.Start)
	}
	if reflectorPeers[0].Peers[0].Endpoint != peer.LocalAddr().(*net.UDPAddr).AddrPort().String() {
		t.Fatalf("peer endpoint = %s, want %s", reflectorPeers[0].Peers[0].Endpoint, peer.LocalAddr())
	}
	if reflectorPeers[0].Peers[0].Hub != nil {
		t.Fatalf("mesh peer hub property = %v, want omitted", *reflectorPeers[0].Peers[0].Hub)
	}

	response, err = http.Get("http://" + configuration.HTTPAddress + "/")
	if err != nil {
		t.Fatalf("request root endpoint: %v", err)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("read root response: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("root status = %d, want 404", response.StatusCode)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() returned an error during cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func TestRunWithoutHTTPStopsCleanly(t *testing.T) {
	configuration := testConfiguration(t)
	configuration.HTTPAddress = ""
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, configuration, discardLogger())
	}()
	cancel()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() without metrics returned an error during cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() without metrics did not stop after cancellation")
	}
}

func TestRunForwardsSpokePacketToConfiguredHub(t *testing.T) {
	configuration := testConfiguration(t)
	configuration.HTTPAddress = ""
	hub, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen as hub: %v", err)
	}
	defer hub.Close()
	configuration.HubEndpoint = hub.LocalAddr().(*net.UDPAddr).AddrPort()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, configuration, discardLogger())
	}()
	defer func() {
		cancel()
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("Run() returned an error during shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run() did not stop during shutdown")
		}
	}()

	spoke, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen as spoke: %v", err)
	}
	defer spoke.Close()
	destination := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), configuration.PortRange.Start)
	payload := []byte("through application wiring")
	received, err := exchangeUntilUDPReceived(spoke, destination, hub, payload)
	if err != nil {
		t.Fatalf("wait for configured hub delivery: %v", err)
	}
	if !bytes.Equal(received, payload) {
		t.Fatalf("hub received %q, want %q", received, payload)
	}
}

func TestRunSubscribesPeerToEveryConfiguredPort(t *testing.T) {
	portStart := availableConsecutiveDualStackPorts(t)
	configuration := testConfiguration(t)
	configuration.PortRange = config.PortRange{Start: portStart, End: portStart + 1}
	configuration.SubscribeAllPorts = true

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, configuration, discardLogger())
	}()
	defer func() {
		cancel()
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("Run() returned an error during shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run() did not stop during shutdown")
		}
	}()

	response := waitForHTTP(t, "http://"+configuration.HTTPAddress+"/metrics")
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("read metrics response: %v", err)
	}
	response.Body.Close()

	sourcePortStart := availableConsecutiveDualStackPorts(t)
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: int(sourcePortStart)})
	if err != nil {
		t.Fatalf("listen as peer: %v", err)
	}
	defer peer.Close()
	destination := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), portStart)
	if _, err := peer.WriteToUDPAddrPort([]byte("register peer"), destination); err != nil {
		t.Fatalf("register peer: %v", err)
	}

	reflectorPeers := waitForPeerOnReflectorPorts(t, "http://"+configuration.HTTPAddress+"/peers", 2)
	for _, reflector := range reflectorPeers {
		wantEndpoint := netip.AddrPortFrom(
			netip.MustParseAddr("127.0.0.1"),
			sourcePortStart+(reflector.ReflectorPort-portStart),
		).String()
		if len(reflector.Peers) != 1 || reflector.Peers[0].Endpoint != wantEndpoint {
			t.Fatalf("reflector port %d peers = %+v, want %s", reflector.ReflectorPort, reflector.Peers, wantEndpoint)
		}
	}
}

func TestRunReleasesHTTPListenerWhenUDPBindFails(t *testing.T) {
	configuration := testConfiguration(t)
	occupied, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.ParseIP("::1"), Port: int(configuration.PortRange.Start)})
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	defer occupied.Close()

	err = Run(context.Background(), configuration, discardLogger())
	if err == nil {
		t.Fatal("Run() succeeded despite an occupied UDP port")
	}

	rebound, err := net.Listen("tcp", configuration.HTTPAddress)
	if err != nil {
		t.Fatalf("HTTP listener was not released after startup failure: %v", err)
	}
	rebound.Close()
}

func TestUnexpectedComponentErrorIgnoresResultDuringCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := unexpectedComponentError(ctx, componentResult{name: "UDP reflector"})

	if err != nil {
		t.Fatalf("unexpectedComponentError() during cancellation = %v, want nil", err)
	}
}

func TestUnexpectedComponentErrorWrapsFailure(t *testing.T) {
	componentError := errors.New("listener failed")

	err := unexpectedComponentError(context.Background(), componentResult{name: "UDP reflector", err: componentError})

	if !errors.Is(err, componentError) || err.Error() != "UDP reflector failed: listener failed" {
		t.Fatalf("unexpectedComponentError() = %v, want wrapped component failure", err)
	}
}

func TestUnexpectedComponentErrorReportsUnexpectedStop(t *testing.T) {
	err := unexpectedComponentError(context.Background(), componentResult{name: "HTTP server"})

	if err == nil || err.Error() != "HTTP server stopped unexpectedly" {
		t.Fatalf("unexpectedComponentError() = %v, want unexpected stop", err)
	}
}

func testConfiguration(t *testing.T) config.Config {
	t.Helper()
	port := availableDualStackPort(t)
	return config.Config{
		PortRange:       config.PortRange{Start: port, End: port},
		PeerTimeout:     time.Minute,
		MaxPeersPerPort: 4,
		UDP4Address:     netip.MustParseAddr("127.0.0.1"),
		UDP6Address:     netip.MustParseAddr("::1"),
		HTTPAddress:     availableTCPAddress(t),
	}
}

func availableTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find available TCP port: %v", err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
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
	t.Helper()
	for candidate := uint16(30000); candidate < 60000; candidate += 2 {
		var connections []*net.UDPConn
		available := true
		for port := candidate; port <= candidate+1; port++ {
			for _, listen := range []struct {
				network string
				address string
			}{{network: "udp4", address: "127.0.0.1"}, {network: "udp6", address: "::1"}} {
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
	t.Fatal("could not find consecutive dual-stack UDP ports")
	return 0
}

func waitForHTTP(t *testing.T, address string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(address)
		if err == nil {
			return response
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("HTTP endpoint %s did not become available", address)
	return nil
}

func waitForReflectorPeers(t *testing.T, address string, peerCount int) []reflectorPeersResponse {
	t.Helper()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(address)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var reflectorPeers []reflectorPeersResponse
		decodeErr := json.NewDecoder(response.Body).Decode(&reflectorPeers)
		response.Body.Close()
		if response.StatusCode == http.StatusOK && decodeErr == nil && len(reflectorPeers) == 1 && len(reflectorPeers[0].Peers) == peerCount {
			return reflectorPeers
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("peer API %s did not report %d peers", address, peerCount)
	return nil
}

func waitForPeerOnReflectorPorts(t *testing.T, address string, portCount int) []reflectorPeersResponse {
	t.Helper()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(address)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var reflectorPeers []reflectorPeersResponse
		decodeErr := json.NewDecoder(response.Body).Decode(&reflectorPeers)
		response.Body.Close()
		if response.StatusCode == http.StatusOK && decodeErr == nil && len(reflectorPeers) == portCount {
			allSubscribed := true
			for _, reflector := range reflectorPeers {
				if len(reflector.Peers) != 1 {
					allSubscribed = false
					break
				}
			}
			if allSubscribed {
				return reflectorPeers
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("peer API %s did not report one peer on %d ports", address, portCount)
	return nil
}

func exchangeUntilUDPReceived(sender *net.UDPConn, destination netip.AddrPort, recipient *net.UDPConn, payload []byte) ([]byte, error) {
	buffer := make([]byte, 65535)
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

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
