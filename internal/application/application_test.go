package application

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"udpreflector/internal/config"
)

func TestRunServesMetricsOnlyAndStopsCleanly(t *testing.T) {
	configuration := testConfiguration(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, configuration, discardLogger())
	}()

	metricsURL := "http://" + configuration.MetricsAddress + "/metrics"
	response := waitForHTTP(t, metricsURL)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", response.StatusCode)
	}
	response.Body.Close()

	response, err := http.Get("http://" + configuration.MetricsAddress + "/")
	if err != nil {
		t.Fatalf("request root endpoint: %v", err)
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
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func TestRunWithoutMetricsStopsCleanly(t *testing.T) {
	configuration := testConfiguration(t)
	configuration.MetricsAddress = ""
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

func TestRunReleasesMetricsListenerWhenUDPBindFails(t *testing.T) {
	configuration := testConfiguration(t)
	occupied, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.ParseIP("::1"), Port: int(configuration.PortStart)})
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	defer occupied.Close()

	err = Run(context.Background(), configuration, discardLogger())
	if err == nil {
		t.Fatal("Run() succeeded despite an occupied UDP port")
	}

	rebound, err := net.Listen("tcp", configuration.MetricsAddress)
	if err != nil {
		t.Fatalf("metrics listener was not released after startup failure: %v", err)
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

func testConfiguration(t *testing.T) config.Config {
	t.Helper()
	port := availableDualStackPort(t)
	return config.Config{
		PortStart:       port,
		PortEnd:         port,
		PeerTimeout:     time.Minute,
		MaxPeersPerPort: 4,
		UDP4Address:     netip.MustParseAddr("127.0.0.1"),
		UDP6Address:     netip.MustParseAddr("::1"),
		MetricsAddress:  availableTCPAddress(t),
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

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
