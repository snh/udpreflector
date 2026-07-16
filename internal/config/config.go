package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"
)

const (
	defaultPeerTimeout = time.Minute
	defaultMaxPeers    = 256
	defaultUDP4Address = "0.0.0.0"
	defaultUDP6Address = "::"
)

type Config struct {
	PortStart       uint16
	PortEnd         uint16
	PeerTimeout     time.Duration
	MaxPeersPerPort int
	UDP4Address     netip.Addr
	UDP6Address     netip.Addr
	MetricsAddress  string
}

func Parse(args []string) (Config, error) {
	flags := flag.NewFlagSet("udpreflector", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var raw struct {
		portStart       int
		portEnd         int
		peerTimeout     time.Duration
		maxPeersPerPort int
		udp4Address     string
		udp6Address     string
		metricsAddress  string
	}

	flags.IntVar(&raw.portStart, "port-start", 0, "first UDP port to listen on")
	flags.IntVar(&raw.portEnd, "port-end", 0, "last UDP port to listen on")
	flags.DurationVar(&raw.peerTimeout, "peer-timeout", defaultPeerTimeout, "peer inactivity timeout")
	flags.IntVar(&raw.maxPeersPerPort, "max-peers-per-port", defaultMaxPeers, "maximum active peers per UDP port")
	flags.StringVar(&raw.udp4Address, "udp4-address", defaultUDP4Address, "IPv4 address to listen on")
	flags.StringVar(&raw.udp6Address, "udp6-address", defaultUDP6Address, "IPv6 address to listen on")
	flags.StringVar(&raw.metricsAddress, "metrics-address", "", "Prometheus HTTP listen address (disabled when omitted)")

	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}

	provided := make(map[string]bool)
	flags.Visit(func(current *flag.Flag) {
		provided[current.Name] = true
	})
	if !provided["port-start"] || !provided["port-end"] {
		return Config{}, errors.New("both --port-start and --port-end are required")
	}
	if raw.portStart < 1 || raw.portStart > 65535 {
		return Config{}, fmt.Errorf("--port-start must be between 1 and 65535: %d", raw.portStart)
	}
	if raw.portEnd < 1 || raw.portEnd > 65535 {
		return Config{}, fmt.Errorf("--port-end must be between 1 and 65535: %d", raw.portEnd)
	}
	if raw.portStart > raw.portEnd {
		return Config{}, errors.New("--port-start must not be greater than --port-end")
	}
	if raw.peerTimeout <= 0 {
		return Config{}, errors.New("--peer-timeout must be positive")
	}
	if raw.maxPeersPerPort <= 0 {
		return Config{}, errors.New("--max-peers-per-port must be positive")
	}

	udp4Address, err := netip.ParseAddr(raw.udp4Address)
	if err != nil || !udp4Address.Is4() {
		return Config{}, fmt.Errorf("--udp4-address must be an IPv4 address: %q", raw.udp4Address)
	}
	udp6Address, err := netip.ParseAddr(raw.udp6Address)
	if err != nil || !udp6Address.Is6() || udp6Address.Is4In6() {
		return Config{}, fmt.Errorf("--udp6-address must be an IPv6 address: %q", raw.udp6Address)
	}
	if provided["metrics-address"] {
		if err := validateListenAddress(raw.metricsAddress); err != nil {
			return Config{}, fmt.Errorf("invalid --metrics-address: %w", err)
		}
	}

	return Config{
		PortStart:       uint16(raw.portStart),
		PortEnd:         uint16(raw.portEnd),
		PeerTimeout:     raw.peerTimeout,
		MaxPeersPerPort: raw.maxPeersPerPort,
		UDP4Address:     udp4Address,
		UDP6Address:     udp6Address,
		MetricsAddress:  raw.metricsAddress,
	}, nil
}

func validateListenAddress(address string) error {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if host == "" {
		return errors.New("host must not be empty")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535: %q", rawPort)
	}
	return nil
}
