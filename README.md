# UDP Reflector

UDP Reflector listens on an inclusive range of UDP ports and remembers each source IP address and source UDP port that sends a packet. It forwards each accepted packet, unchanged, to every other peer heard on that logical listening port within the inactivity timeout.

Each logical port has separate IPv4 and IPv6 sockets but one shared peer set, so an IPv4 sender can reach recent IPv6 peers and vice versa. The exact sender endpoint never receives its own packet. A different source port on the same IP address is a distinct peer.

## Behaviour and limits

- Both ends of the listening range are required; there is no implicit UDP port range.
- Peer activity expires after 60 seconds by default. Every accepted packet refreshes its sender.
- Each logical port accepts at most 256 active peers by default.
- A packet from a new peer at capacity is dropped without being forwarded. Existing peers remain admitted until they expire.
- Packets are forwarded as independent UDP datagrams without transformation. UDP delivery, ordering, and uniqueness are not guaranteed.
- Startup is atomic. If any requested IPv4 socket, IPv6 socket, or configured metrics listener cannot bind, the process closes all listeners and exits.
- Both address families must be available. The service does not silently fall back to partial coverage.

The peer cap bounds memory and per-packet fan-out, but this remains a UDP reflection service. Source addresses can be spoofed on networks that do not apply ingress filtering. Restrict access with host or network firewalls and deploy only on networks where reflection traffic is intended.

## Build and run

Go 1.26.5 or later is required.

```sh
make test
make build
./dist/udpreflector --version
./dist/udpreflector --port-start=40000 --port-end=40100
```

Build identifiers use the UTC build date and seven-character Git commit ID, for example `20260717-a1b2c3d`. Local builds with uncommitted changes append `-dirty`; an ordinary `go build` reports `dev`.

The service listens on all IPv4 and IPv6 interfaces by default. Prometheus metrics are disabled unless `--metrics-address` is specified:

```sh
./dist/udpreflector --port-start=40000 --port-end=40100 --metrics-address=127.0.0.1:9090
```

### Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--help` | off | Show command usage and exit |
| `--version` | off | Show build identifier and exit |
| `--port-start` | required | First UDP port in the inclusive range |
| `--port-end` | required | Last UDP port in the inclusive range |
| `--peer-timeout` | `1m` | Inactivity period before a peer expires |
| `--max-peers-per-port` | `256` | Maximum active peers on each logical port |
| `--udp4-address` | `0.0.0.0` | IPv4 listening address |
| `--udp6-address` | `::` | IPv6 listening address |
| `--metrics-address` | disabled | Prometheus HTTP listening address |

When enabled, only `/metrics` is served by the HTTP listener. Keep it on loopback unless remote scraping is required and protected.

## Prometheus metrics

Pass `--metrics-address` to enable standard Go and process collectors together with:

- `udp_reflector_packets_received_total`
- `udp_reflector_bytes_received_total`
- `udp_reflector_packets_forwarded_total`
- `udp_reflector_bytes_forwarded_total`
- `udp_reflector_packets_dropped_total`
- `udp_reflector_send_errors_total`
- `udp_reflector_listener_errors_total`
- `udp_reflector_listener_up`
- `udp_reflector_active_peers`

Metric labels contain only the configured listening port, address family, and controlled drop reason. Peer addresses and source ports are never labels.

## Docker

The image contains a static binary and runs as numeric user `65532`. Publish every selected UDP port for both address families as supported by the container runtime. Metrics must bind beyond container loopback when they are published.

```sh
docker build -t udpreflector .
docker run --rm \
  -p 40000-40010:40000-40010/udp \
  -p 127.0.0.1:9090:9090/tcp \
  udpreflector \
  --port-start=40000 \
  --port-end=40010 \
  --metrics-address=:9090
```

The Dockerfile exposes only the conventional optional metrics TCP port because the UDP range is selected at runtime. `EXPOSE` does not enable metrics, publish ports, or replace explicit `-p` options.

## Debian packages

Build static `amd64` and `arm64` packages with:

```sh
make debian-packages
```

This creates `dist/udpreflector_<build-identifier>_amd64.deb` and `dist/udpreflector_<build-identifier>_arm64.deb`. Building packages requires `dpkg-deb`; on Debian and Ubuntu it is provided by the `dpkg` package.

Each package installs the binary at `/usr/bin/udpreflector`, the systemd unit at `/usr/lib/systemd/system/udpreflector.service`, the manual page, README, and licence. The service is not enabled automatically; review its port range and metrics setting before enabling it.

## systemd

The example in [`contrib/systemd/udpreflector.service`](contrib/systemd/udpreflector.service) uses dynamic credentials, an unprivileged example range, no Linux capabilities, and filesystem and process hardening suitable for a stateless network service.

Install the binary and unit, review the port range, then enable it:

```sh
sudo install -m 0755 dist/udpreflector /usr/bin/udpreflector
sudo install -m 0644 contrib/systemd/udpreflector.service /etc/systemd/system/udpreflector.service
sudo systemctl daemon-reload
sudo systemctl enable --now udpreflector.service
```

Ports below 1024 require `CAP_NET_BIND_SERVICE`. Add that capability only when a privileged range is genuinely required. The example deliberately grants no capabilities.

## Capacity planning

The process opens two UDP sockets per configured port, plus one TCP listener when metrics are enabled. Allow at least $2n$ file descriptors for $n$ UDP ports, or $2n + 1$ with metrics, with additional headroom for the runtime and HTTP connections. The systemd example allows 4096 descriptors.

Firewall rules must permit the entire UDP range for both IPv4 and IPv6 where both families are expected. A large port range also creates two receive loops and two sockets per port, so select only the range the application needs.

## Continuous integration

The GitHub Actions build runs static analysis, normal tests, and race tests, then produces Linux `amd64`, Linux `arm64`, macOS `arm64`, and Windows `amd64` binaries. Linux builds also produce matching Debian packages. Each output is uploaded as a workflow artefact; container images are not built by the workflow.

## Licence

UDP Reflector is available under the [MIT License](LICENSE).

## Development sandbox

Local macOS development follows [`.github/copilot-instructions.md`](.github/copilot-instructions.md). All package installation, formatting, builds, tests, and application processes run inside the reusable Alpine Lima VM named `udpreflector-sandbox`.

Enter the mounted repository in the guest and run the checks there:

```sh
limactl shell udpreflector-sandbox
cd /Users/snh/Documents/Code/udpreflector
gofmt -w ./cmd ./internal
GOTOOLCHAIN=auto make vet
GOTOOLCHAIN=auto make test
GOTOOLCHAIN=auto make test-race
GOTOOLCHAIN=auto make build
```

The repository is mounted at the same absolute path in the guest. Alpine's packaged Go toolchain may default to `GOTOOLCHAIN=local`; `GOTOOLCHAIN=auto` selects and caches the Go 1.26.5 toolchain required by the module. Do not recreate an existing sandbox merely to update tools; install missing packages with Alpine's package manager.
