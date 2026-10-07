package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Lease is one DHCP server entry, either a configured reservation or an
// active lease.
type Lease struct {
	MAC      string
	IPv4     string
	Hostname string
	Source   Source
	// Expiry is the zero time on a reservation, which is configuration rather
	// than a granted lease. The same client's active lease carries the real
	// expiry, and Merge joins the two on the MAC.
	Expiry time.Time
}

// networkctl --json describes a single interface as a bare object, but the
// no-argument form wraps a list under "Interfaces". Accept both so the
// collector is not tied to one invocation shape.
type networkctlOutput struct {
	DHCPServer *dhcpServer        `json:"DHCPServer"`
	Interfaces []networkctlOutput `json:"Interfaces"`
}

type dhcpServer struct {
	Leases       []dhcpLease `json:"Leases"`
	StaticLeases []dhcpLease `json:"StaticLeases"`
}

// Addresses arrive as arrays of byte-valued numbers, so they are decoded as
// []int — encoding/json only fills a []byte from a base64 string.
type dhcpLease struct {
	AddressString   string `json:"AddressString"`
	Hostname        string `json:"Hostname"`
	ClientID        []int  `json:"ClientId"`
	HardwareAddress []int  `json:"HardwareAddress"`
	// Wall-clock microseconds. There is also an ExpirationUSec on the
	// monotonic clock, which is useless across a reboot.
	ExpirationRealtimeUSec int64 `json:"ExpirationRealtimeUSec"`
}

// Leases runs networkctl and returns the server's reservations and active
// leases for one interface.
func Leases(ctx context.Context, iface string) ([]Lease, error) {
	out, err := run(ctx, "networkctl", "--json=short", "status", iface)
	if err != nil {
		return nil, err
	}
	return ParseLeases(out)
}

// ParseLeases decodes `networkctl --json` output. Static leases come first so
// that Merge's precedence rule never depends on map iteration order.
func ParseLeases(data []byte) ([]Lease, error) {
	var out networkctlOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding networkctl output: %w", err)
	}

	servers := []*dhcpServer{}
	if out.DHCPServer != nil {
		servers = append(servers, out.DHCPServer)
	}
	for _, iface := range out.Interfaces {
		if iface.DHCPServer != nil {
			servers = append(servers, iface.DHCPServer)
		}
	}

	leases := []Lease{}
	for _, s := range servers {
		leases = append(leases, convert(s.StaticLeases, SourceStatic)...)
		leases = append(leases, convert(s.Leases, SourceDHCP)...)
	}
	return leases, nil
}

func convert(in []dhcpLease, source Source) []Lease {
	out := make([]Lease, 0, len(in))
	for _, l := range in {
		mac := leaseMAC(l)
		if mac == "" {
			continue
		}
		var expiry time.Time
		if l.ExpirationRealtimeUSec > 0 {
			expiry = time.UnixMicro(l.ExpirationRealtimeUSec)
		}
		out = append(out, Lease{
			MAC:      mac,
			IPv4:     l.AddressString,
			Hostname: l.Hostname,
			Source:   source,
			Expiry:   expiry,
		})
	}
	return out
}

// leaseMAC pulls the hardware address out of a lease. Active leases carry it
// directly, but static leases report an empty HardwareAddress and keep the MAC
// inside the client identifier: a type byte followed by the address, with type
// 1 meaning ethernet (RFC 2132 §9.14). Clients that send a DUID-based
// identifier instead (type 255, RFC 4361) always have HardwareAddress set, so
// nothing needs to unpack those.
func leaseMAC(l dhcpLease) string {
	if len(l.HardwareAddress) == 6 {
		return formatMAC(l.HardwareAddress)
	}
	if len(l.ClientID) == 7 && l.ClientID[0] == 1 {
		return formatMAC(l.ClientID[1:])
	}
	return ""
}

func formatMAC(b []int) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02x", byte(v))
	}
	return strings.Join(parts, ":")
}
