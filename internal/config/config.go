package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPeerTimeout = time.Minute
	defaultMaxPeers    = 256
	defaultUDP4Address = "0.0.0.0"
	defaultUDP6Address = "::"
)

type PortRange struct {
	Start uint16
	End   uint16
}

type Config struct {
	PortRange         PortRange
	HubEndpoint       netip.AddrPort
	SubscribeAllPorts bool
	PeerTimeout       time.Duration
	MaxPeersPerPort   int
	UDP4Address       netip.Addr
	UDP6Address       netip.Addr
	HTTPAddress       string
}

type rawConfig struct {
	portRange         string
	hubEndpoint       string
	subscribeAllPorts bool
	peerTimeout       time.Duration
	maxPeersPerPort   int
	udp4Address       string
	udp6Address       string
	httpAddress       string
}

func newFlagSet(output io.Writer) (*flag.FlagSet, *rawConfig) {
	flags := flag.NewFlagSet("udpreflector", flag.ContinueOnError)
	flags.SetOutput(output)
	raw := new(rawConfig)

	flags.StringVar(&raw.portRange, "port-range", "", "inclusive UDP port range (START-END)")
	flags.StringVar(&raw.hubEndpoint, "hub-endpoint", "", "hub IP and optional base port")
	flags.BoolVar(&raw.subscribeAllPorts, "subscribe-all-ports", false, "subscribe a peer to every configured UDP port")
	flags.DurationVar(&raw.peerTimeout, "peer-timeout", defaultPeerTimeout, "peer inactivity timeout")
	flags.IntVar(&raw.maxPeersPerPort, "max-peers-per-port", defaultMaxPeers, "maximum active peers per logical port")
	flags.StringVar(&raw.udp4Address, "udp4-address", defaultUDP4Address, "IPv4 address to listen on")
	flags.StringVar(&raw.udp6Address, "udp6-address", defaultUDP6Address, "IPv6 address to listen on")
	flags.StringVar(&raw.httpAddress, "http-address", "", "peer API and metrics HTTP listen address (disabled when omitted)")

	return flags, raw
}

func PrintDefaults(output io.Writer) {
	flags, _ := newFlagSet(output)
	flags.PrintDefaults()
}

func Parse(args []string) (Config, error) {
	flags, raw := newFlagSet(io.Discard)

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
	if !provided["port-range"] {
		return Config{}, errors.New("--port-range is required")
	}
	portRange, err := parsePortRange(raw.portRange)
	if err != nil {
		return Config{}, fmt.Errorf("invalid --port-range: %w", err)
	}

	var hubEndpoint netip.AddrPort
	if provided["hub-endpoint"] {
		hubEndpoint, err = parseHubEndpoint(raw.hubEndpoint)
		if err != nil {
			return Config{}, fmt.Errorf("invalid --hub-endpoint: %w", err)
		}
		if hubEndpoint.Port() != 0 {
			lastHubPort := uint32(hubEndpoint.Port()) + uint32(portRange.End-portRange.Start)
			if lastHubPort > 65535 {
				return Config{}, fmt.Errorf("hub port mapping ends above 65535: %d", lastHubPort)
			}
		}
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
	if provided["http-address"] {
		if err := validateListenAddress(raw.httpAddress); err != nil {
			return Config{}, fmt.Errorf("invalid --http-address: %w", err)
		}
	}

	return Config{
		PortRange:         portRange,
		HubEndpoint:       hubEndpoint,
		SubscribeAllPorts: raw.subscribeAllPorts,
		PeerTimeout:       raw.peerTimeout,
		MaxPeersPerPort:   raw.maxPeersPerPort,
		UDP4Address:       udp4Address,
		UDP6Address:       udp6Address,
		HTTPAddress:       raw.httpAddress,
	}, nil
}

func parsePortRange(value string) (PortRange, error) {
	if strings.Count(value, "-") != 1 {
		return PortRange{}, errors.New("must use START-END")
	}
	rawStart, rawEnd, _ := strings.Cut(value, "-")
	start, err := parsePort(rawStart)
	if err != nil {
		return PortRange{}, fmt.Errorf("invalid start port: %w", err)
	}
	end, err := parsePort(rawEnd)
	if err != nil {
		return PortRange{}, fmt.Errorf("invalid end port: %w", err)
	}
	if start > end {
		return PortRange{}, errors.New("start port must not be greater than end port")
	}
	return PortRange{Start: start, End: end}, nil
}

func parsePort(value string) (uint16, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("must be between 1 and 65535: %q", value)
	}
	return uint16(port), nil
}

func parseHubEndpoint(value string) (netip.AddrPort, error) {
	if endpoint, err := netip.ParseAddrPort(value); err == nil {
		return validateHubEndpoint(endpoint)
	}

	rawAddress := value
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		rawAddress = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	}
	address, err := netip.ParseAddr(rawAddress)
	if err != nil {
		return netip.AddrPort{}, errors.New("must be a literal IP with an optional port")
	}
	address = address.Unmap()
	if address.IsUnspecified() {
		return netip.AddrPort{}, errors.New("IP address must not be unspecified")
	}
	return netip.AddrPortFrom(address, 0), nil
}

func validateHubEndpoint(endpoint netip.AddrPort) (netip.AddrPort, error) {
	address := endpoint.Addr().Unmap()
	if !address.IsValid() || address.IsUnspecified() {
		return netip.AddrPort{}, errors.New("IP address must not be unspecified")
	}
	if endpoint.Port() == 0 {
		return netip.AddrPort{}, errors.New("port must be between 1 and 65535")
	}
	return netip.AddrPortFrom(address, endpoint.Port()), nil
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
