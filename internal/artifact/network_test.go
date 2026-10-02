package artifact

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRepositoryAddressesRejectUnsafeRanges(t *testing.T) {
	for _, address := range []string{
		"127.0.0.1", "::1", "::ffff:127.0.0.1",
		"169.254.169.254", "fe80::1", "fe80::1%eth0",
		"0.0.0.0", "::", "224.0.0.1", "ff02::1", "255.255.255.255",
	} {
		t.Run(address, func(t *testing.T) {
			// Given: a destination outside the permitted unicast network boundary.
			literal, zone, _ := strings.Cut(address, "%")
			// When: validating the exact address set used before dialing.
			err := validateRepositoryIPs(
				[]net.IPAddr{{IP: net.ParseIP(literal), Zone: zone}},
			)
			// Then: rejection does not depend on connection failure or timeout.
			if !errors.Is(err, ErrFetch) {
				t.Fatalf("unsafe destination accepted: %v", err)
			}
		})
	}
}

func TestRepositoryDialRejectsReachableLoopback(t *testing.T) {
	// Given: a reachable loopback service, including its mapped IPv6 alias.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"127.0.0.1", "::ffff:127.0.0.1"} {
		t.Run(ip, func(t *testing.T) {
			address := net.JoinHostPort(ip, port)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			probe, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
			if err != nil {
				t.Fatalf("fixture is unreachable: %v", err)
			}
			probe.Close()
			// When: the production boundary receives the same reachable address.
			conn, err := dialRepository(ctx, "tcp", address)
			if conn != nil {
				conn.Close()
			}
			// Then: it blocks access even though an ordinary dial succeeds.
			if !errors.Is(err, ErrFetch) {
				t.Fatalf("reachable loopback accepted: %v", err)
			}
		})
	}
}

func TestRepositoryDialRejectsMixedDNSAnswers(t *testing.T) {
	// Given: one permitted private address and one forbidden loopback answer.
	ip := artifactTestIP(t)
	var lookups atomic.Int32
	installArtifactDNS(
		t,
		func() []net.IP {
			lookups.Add(1)
			return []net.IP{ip, net.ParseIP("127.0.0.1")}
		},
	)
	listener, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// When: a repository hostname resolves to mixed answers.
	conn, err := dialRepository(t.Context(), "tcp", "repo.invalid:"+port)
	if conn != nil {
		conn.Close()
	}
	// Then: the whole answer set is rejected, not just the unsafe entry.
	if !errors.Is(err, ErrFetch) || lookups.Load() == 0 {
		t.Fatalf("mixed DNS destination accepted: %v", err)
	}
}

func TestRepositoryDialPinsValidatedIPAcrossRebinding(t *testing.T) {
	// Given: DNS switches to loopback immediately after its first A answer.
	ip := artifactTestIP(t)
	var lookups atomic.Int32
	installArtifactDNS(t, func() []net.IP {
		if lookups.Add(1) == 1 {
			return []net.IP{ip}
		}
		return []net.IP{net.ParseIP("127.0.0.1")}
	})
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// When: the production dialer validates and connects to the hostname.
	conn, err := dialRepository(t.Context(), "tcp", "repo.invalid:"+port)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Then: it connects to the validated IP without another DNS lookup.
	if !strings.HasPrefix(conn.RemoteAddr().String(), ip.String()+":") ||
		lookups.Load() != 1 {
		t.Fatalf(
			"destination was re-resolved: remote=%s A-lookups=%d",
			conn.RemoteAddr(),
			lookups.Load(),
		)
	}
	rebound, err := net.DefaultResolver.LookupIPAddr(
		t.Context(),
		"repo.invalid",
	)
	if err != nil || len(rebound) != 1 || !rebound[0].IP.IsLoopback() {
		t.Fatalf("DNS fixture did not rebind: %v, %v", rebound, err)
	}
}

func artifactTestIP(t *testing.T) net.IP {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() != nil && ip.IsGlobalUnicast() &&
			!ip.IsLoopback() &&
			!ip.IsLinkLocalUnicast() {
			return ip
		}
	}
	t.Fatal("artifact network tests require a non-loopback IPv4 interface")
	return nil
}
