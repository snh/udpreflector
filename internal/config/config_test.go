package config

import (
	"net/netip"
	"testing"
	"time"
)

func TestParseValidConfiguration(t *testing.T) {
	configuration, err := Parse([]string{
		"--port-start=40000",
		"--port-end=40010",
		"--peer-timeout=30s",
		"--max-peers-per-port=64",
		"--udp4-address=127.0.0.1",
		"--udp6-address=::1",
		"--metrics-address=localhost:9100",
	})
	if err != nil {
		t.Fatalf("Parse() returned an unexpected error: %v", err)
	}

	if configuration.PortStart != 40000 || configuration.PortEnd != 40010 {
		t.Fatalf("unexpected port range: %d-%d", configuration.PortStart, configuration.PortEnd)
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
	configuration, err := Parse([]string{"--port-start=40000", "--port-end=40000"})
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
}

func TestParseRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing both port bounds"},
		{name: "missing ending port", args: []string{"--port-start=40000"}},
		{name: "missing starting port", args: []string{"--port-end=40000"}},
		{name: "zero starting port", args: []string{"--port-start=0", "--port-end=1"}},
		{name: "ending port above range", args: []string{"--port-start=1", "--port-end=65536"}},
		{name: "reversed port range", args: []string{"--port-start=40001", "--port-end=40000"}},
		{name: "zero peer timeout", args: []string{"--port-start=1", "--port-end=1", "--peer-timeout=0s"}},
		{name: "negative peer capacity", args: []string{"--port-start=1", "--port-end=1", "--max-peers-per-port=-1"}},
		{name: "IPv6 address used for IPv4", args: []string{"--port-start=1", "--port-end=1", "--udp4-address=::1"}},
		{name: "IPv4 address used for IPv6", args: []string{"--port-start=1", "--port-end=1", "--udp6-address=127.0.0.1"}},
		{name: "mapped address used for IPv6", args: []string{"--port-start=1", "--port-end=1", "--udp6-address=::ffff:127.0.0.1"}},
		{name: "metrics address without port", args: []string{"--port-start=1", "--port-end=1", "--metrics-address=localhost"}},
		{name: "metrics address with empty host", args: []string{"--port-start=1", "--port-end=1", "--metrics-address=:9090"}},
		{name: "metrics address with invalid port", args: []string{"--port-start=1", "--port-end=1", "--metrics-address=localhost:70000"}},
		{name: "explicit empty metrics address", args: []string{"--port-start=1", "--port-end=1", "--metrics-address="}},
		{name: "unexpected positional argument", args: []string{"--port-start=1", "--port-end=1", "extra"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse(test.args); err == nil {
				t.Fatal("Parse() returned nil error")
			}
		})
	}
}
