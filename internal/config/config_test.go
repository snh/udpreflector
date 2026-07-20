package config

import (
	"net/netip"
	"testing"
	"time"
)

func TestParseValidConfiguration(t *testing.T) {
	configuration, err := Parse([]string{
		"--port-range=40000-40010",
		"--hub-endpoint=192.0.2.1:30000",
		"--peer-timeout=30s",
		"--max-peers-per-port=64",
		"--udp4-address=127.0.0.1",
		"--udp6-address=::1",
		"--metrics-address=localhost:9100",
	})
	if err != nil {
		t.Fatalf("Parse() returned an unexpected error: %v", err)
	}

	if configuration.PortRange != (PortRange{Start: 40000, End: 40010}) {
		t.Fatalf("unexpected port range: %+v", configuration.PortRange)
	}
	if configuration.HubEndpoint != netip.MustParseAddrPort("192.0.2.1:30000") {
		t.Fatalf("unexpected hub endpoint: %s", configuration.HubEndpoint)
	}
	if configuration.PeerTimeout != 30*time.Second {
		t.Fatalf("unexpected peer timeout: %s", configuration.PeerTimeout)
	}
	if configuration.MaxPeersPerPort != 64 {
		t.Fatalf("unexpected maximum peers: %d", configuration.MaxPeersPerPort)
	}
	if configuration.UDP4Address != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("unexpected IPv4 address: %s", configuration.UDP4Address)
	}
	if configuration.UDP6Address != netip.MustParseAddr("::1") {
		t.Fatalf("unexpected IPv6 address: %s", configuration.UDP6Address)
	}
	if configuration.MetricsAddress != "localhost:9100" {
		t.Fatalf("unexpected metrics address: %s", configuration.MetricsAddress)
	}
}

func TestParseDefaults(t *testing.T) {
	configuration, err := Parse([]string{"--port-range=40000-40000"})
	if err != nil {
		t.Fatalf("Parse() returned an unexpected error: %v", err)
	}

	if configuration.PeerTimeout != time.Minute {
		t.Errorf("PeerTimeout = %s, want 1m", configuration.PeerTimeout)
	}
	if configuration.MaxPeersPerPort != 256 {
		t.Errorf("MaxPeersPerPort = %d, want 256", configuration.MaxPeersPerPort)
	}
	if configuration.MetricsAddress != "" {
		t.Errorf("MetricsAddress = %q, want metrics disabled", configuration.MetricsAddress)
	}
	if configuration.HubEndpoint.IsValid() {
		t.Errorf("HubEndpoint = %s, want hub mode disabled", configuration.HubEndpoint)
	}
}

func TestParseHubEndpointWithoutPortRemainsDynamic(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{name: "IPv4", value: "192.0.2.1", expected: "192.0.2.1:0"},
		{name: "bare IPv6", value: "2001:db8::1", expected: "[2001:db8::1]:0"},
		{name: "bracketed IPv6", value: "[2001:db8::1]", expected: "[2001:db8::1]:0"},
		{name: "mapped IPv4", value: "::ffff:192.0.2.1", expected: "192.0.2.1:0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration, err := Parse([]string{"--port-range=40000-40010", "--hub-endpoint=" + test.value})
			if err != nil {
				t.Fatalf("Parse() returned an unexpected error: %v", err)
			}
			if configuration.HubEndpoint != netip.MustParseAddrPort(test.expected) {
				t.Fatalf("HubEndpoint = %s, want %s", configuration.HubEndpoint, test.expected)
			}
		})
	}
}

func TestParseAcceptsHubMappingEndingAtMaximumPort(t *testing.T) {
	configuration, err := Parse([]string{"--port-range=40000-40010", "--hub-endpoint=192.0.2.1:65525"})
	if err != nil {
		t.Fatalf("Parse() returned an unexpected error: %v", err)
	}
	if configuration.HubEndpoint.Port() != 65525 {
		t.Fatalf("HubEndpoint port = %d, want 65525", configuration.HubEndpoint.Port())
	}
}

func TestParseRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing port range"},
		{name: "explicit empty port range", args: []string{"--port-range="}},
		{name: "missing ending port", args: []string{"--port-range=40000-"}},
		{name: "missing starting port", args: []string{"--port-range=-40000"}},
		{name: "extra separator", args: []string{"--port-range=1-2-3"}},
		{name: "zero starting port", args: []string{"--port-range=0-1"}},
		{name: "ending port above range", args: []string{"--port-range=1-65536"}},
		{name: "reversed port range", args: []string{"--port-range=40001-40000"}},
		{name: "removed starting flag", args: []string{"--port-start=40000"}},
		{name: "removed ending flag", args: []string{"--port-end=40000"}},
		{name: "zero peer timeout", args: []string{"--port-range=1-1", "--peer-timeout=0s"}},
		{name: "negative peer capacity", args: []string{"--port-range=1-1", "--max-peers-per-port=-1"}},
		{name: "IPv6 address used for IPv4", args: []string{"--port-range=1-1", "--udp4-address=::1"}},
		{name: "IPv4 address used for IPv6", args: []string{"--port-range=1-1", "--udp6-address=127.0.0.1"}},
		{name: "mapped address used for IPv6", args: []string{"--port-range=1-1", "--udp6-address=::ffff:127.0.0.1"}},
		{name: "metrics address without port", args: []string{"--port-range=1-1", "--metrics-address=localhost"}},
		{name: "metrics address with empty host", args: []string{"--port-range=1-1", "--metrics-address=:9090"}},
		{name: "metrics address with invalid port", args: []string{"--port-range=1-1", "--metrics-address=localhost:70000"}},
		{name: "explicit empty metrics address", args: []string{"--port-range=1-1", "--metrics-address="}},
		{name: "explicit empty hub endpoint", args: []string{"--port-range=1-1", "--hub-endpoint="}},
		{name: "hub hostname", args: []string{"--port-range=1-1", "--hub-endpoint=example.com"}},
		{name: "unspecified IPv4 hub", args: []string{"--port-range=1-1", "--hub-endpoint=0.0.0.0"}},
		{name: "unspecified IPv6 hub", args: []string{"--port-range=1-1", "--hub-endpoint=::"}},
		{name: "hub port zero", args: []string{"--port-range=1-1", "--hub-endpoint=192.0.2.1:0"}},
		{name: "hub mapping overflow", args: []string{"--port-range=40000-40010", "--hub-endpoint=192.0.2.1:65526"}},
		{name: "unexpected positional argument", args: []string{"--port-range=1-1", "extra"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse(test.args); err == nil {
				t.Fatal("Parse() returned nil error")
			}
		})
	}
}
