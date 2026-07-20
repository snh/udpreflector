package reflector

import (
	"net/netip"
	"testing"
	"time"
)

func TestRegistryFirstPeerHasNoRecipients(t *testing.T) {
	registry := newPeerRegistry(time.Minute, 2, netip.AddrPort{})
	result := registry.admit(endpoint("192.0.2.1:1000"), time.Unix(100, 0))

	if !result.accepted {
		t.Fatal("first peer was rejected")
	}
	if len(result.recipients) != 0 {
		t.Fatalf("first peer received %d recipients, want 0", len(result.recipients))
	}
	if result.active != 1 {
		t.Fatalf("active peers = %d, want 1", result.active)
	}
}

func TestRegistryExcludesOnlyExactSender(t *testing.T) {
	registry := newPeerRegistry(time.Minute, 3, netip.AddrPort{})
	now := time.Unix(100, 0)
	first := endpoint("192.0.2.1:1000")
	second := endpoint("192.0.2.1:1001")
	third := endpoint("[2001:db8::1]:1000")

	registry.admit(first, now)
	registry.admit(second, now)
	result := registry.admit(third, now)

	assertRecipients(t, result.recipients, first, second)
	result = registry.admit(first, now.Add(time.Second))
	assertRecipients(t, result.recipients, second, third)
}

func TestRegistryRefreshExtendsPeerLifetime(t *testing.T) {
	registry := newPeerRegistry(time.Minute, 2, netip.AddrPort{})
	started := time.Unix(100, 0)
	first := endpoint("192.0.2.1:1000")
	second := endpoint("192.0.2.2:1000")

	registry.admit(first, started)
	registry.admit(first, started.Add(30*time.Second))
	result := registry.admit(second, started.Add(61*time.Second))

	assertRecipients(t, result.recipients, first)
	if result.expired != 0 {
		t.Fatalf("expired peers = %d, want 0", result.expired)
	}
}

func TestRegistryExpiresPeerAtTimeout(t *testing.T) {
	registry := newPeerRegistry(time.Minute, 2, netip.AddrPort{})
	started := time.Unix(100, 0)
	registry.admit(endpoint("192.0.2.1:1000"), started)

	expired, active := registry.expire(started.Add(time.Minute))

	if expired != 1 || active != 0 {
		t.Fatalf("expire() = (%d, %d), want (1, 0)", expired, active)
	}
}

func TestRegistryRejectsNewPeerAtCapacity(t *testing.T) {
	registry := newPeerRegistry(time.Minute, 2, netip.AddrPort{})
	now := time.Unix(100, 0)
	first := endpoint("192.0.2.1:1000")
	second := endpoint("192.0.2.2:1000")

	registry.admit(first, now)
	registry.admit(second, now)
	result := registry.admit(endpoint("192.0.2.3:1000"), now)

	if result.accepted {
		t.Fatal("new peer was accepted at capacity")
	}
	if len(result.recipients) != 0 {
		t.Fatalf("rejected peer received %d recipients, want 0", len(result.recipients))
	}
	if result.active != 2 {
		t.Fatalf("active peers = %d, want 2", result.active)
	}

	result = registry.admit(first, now.Add(time.Second))
	if !result.accepted {
		t.Fatal("existing peer was rejected at capacity")
	}
	assertRecipients(t, result.recipients, second)
}

func TestRegistryExpiryFreesCapacity(t *testing.T) {
	registry := newPeerRegistry(time.Minute, 1, netip.AddrPort{})
	started := time.Unix(100, 0)
	registry.admit(endpoint("192.0.2.1:1000"), started)

	result := registry.admit(endpoint("192.0.2.2:1000"), started.Add(time.Minute))

	if !result.accepted {
		t.Fatal("new peer was rejected after expiry freed capacity")
	}
	if result.expired != 1 || result.active != 1 {
		t.Fatalf("admission expiry state = (%d, %d), want (1, 1)", result.expired, result.active)
	}
}

func TestRegistryCanonicalisesMappedIPv4Address(t *testing.T) {
	registry := newPeerRegistry(time.Minute, 1, netip.AddrPort{})
	now := time.Unix(100, 0)
	registry.admit(endpoint("[::ffff:192.0.2.1]:1000"), now)

	result := registry.admit(endpoint("192.0.2.1:1000"), now.Add(time.Second))

	if !result.accepted || result.active != 1 {
		t.Fatalf("mapped peer was not recognised as existing: %+v", result)
	}
}

func TestRegistrySpokeForwardsOnlyToHub(t *testing.T) {
	hub := endpoint("192.0.2.100:30000")
	registry := newPeerRegistry(time.Minute, 2, hub)
	now := time.Unix(100, 0)

	first := registry.admit(endpoint("192.0.2.1:1000"), now)
	second := registry.admit(endpoint("192.0.2.2:1000"), now)

	assertRecipients(t, first.recipients, hub)
	assertRecipients(t, second.recipients, hub)
	if first.active != 1 || second.active != 2 {
		t.Fatalf("active spoke counts = (%d, %d), want (1, 2)", first.active, second.active)
	}
}

func TestRegistryHubForwardsToAllSpokes(t *testing.T) {
	hub := endpoint("192.0.2.100:30000")
	registry := newPeerRegistry(time.Minute, 2, hub)
	now := time.Unix(100, 0)
	first := endpoint("192.0.2.1:1000")
	second := endpoint("192.0.2.2:1000")
	registry.admit(first, now)
	registry.admit(second, now)

	result := registry.admit(hub, now)

	assertRecipients(t, result.recipients, first, second)
	if !result.accepted || result.active != 2 {
		t.Fatalf("hub admission = %+v, want accepted with 2 active spokes", result)
	}
}

func TestRegistryHubRemainsAcceptedAtSpokeCapacity(t *testing.T) {
	hub := endpoint("192.0.2.100:30000")
	registry := newPeerRegistry(time.Minute, 1, hub)
	now := time.Unix(100, 0)
	spoke := endpoint("192.0.2.1:1000")
	registry.admit(spoke, now)

	result := registry.admit(hub, now)

	if !result.accepted {
		t.Fatal("hub was rejected at spoke capacity")
	}
	assertRecipients(t, result.recipients, spoke)
	if result.active != 1 {
		t.Fatalf("active peers = %d, want 1 spoke", result.active)
	}
}

func TestRegistryHubDoesNotReceiveRejectedSpokePacket(t *testing.T) {
	hub := endpoint("192.0.2.100:30000")
	registry := newPeerRegistry(time.Minute, 1, hub)
	now := time.Unix(100, 0)
	registry.admit(endpoint("192.0.2.1:1000"), now)

	result := registry.admit(endpoint("192.0.2.2:1000"), now)

	if result.accepted {
		t.Fatal("new spoke was accepted at capacity")
	}
	assertRecipients(t, result.recipients)
}

func TestRegistryCanonicalisesMappedHubAddress(t *testing.T) {
	hub := endpoint("192.0.2.100:30000")
	registry := newPeerRegistry(time.Minute, 1, hub)
	spoke := endpoint("192.0.2.1:1000")
	now := time.Unix(100, 0)
	registry.admit(spoke, now)

	result := registry.admit(endpoint("[::ffff:192.0.2.100]:30000"), now)

	assertRecipients(t, result.recipients, spoke)
}

func TestRegistryExpiresSpokesBeforeHubFanOut(t *testing.T) {
	hub := endpoint("192.0.2.100:30000")
	registry := newPeerRegistry(time.Minute, 1, hub)
	started := time.Unix(100, 0)
	registry.admit(endpoint("192.0.2.1:1000"), started)

	result := registry.admit(hub, started.Add(time.Minute))

	assertRecipients(t, result.recipients)
	if result.expired != 1 || result.active != 0 {
		t.Fatalf("hub admission expiry state = (%d, %d), want (1, 0)", result.expired, result.active)
	}
}

func endpoint(value string) netip.AddrPort {
	return netip.MustParseAddrPort(value)
}

func assertRecipients(t *testing.T, actual []netip.AddrPort, expected ...netip.AddrPort) {
	t.Helper()
	wanted := make(map[netip.AddrPort]bool, len(expected))
	for _, peer := range expected {
		wanted[peer] = true
	}
	if len(actual) != len(wanted) {
		t.Fatalf("recipients = %v, want %v", actual, expected)
	}
	for _, peer := range actual {
		if !wanted[peer] {
			t.Fatalf("unexpected recipient %s; want %v", peer, expected)
		}
	}
}
