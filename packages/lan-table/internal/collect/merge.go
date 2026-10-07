package collect

import (
	"net/netip"
	"sort"
	"time"
)

// Row is one device: the unit the rendered table shows.
type Row struct {
	Hostname string
	IPv4     string
	IPv6     []string
	Source   Source
	MAC      string
	// LeaseAge is how long ago the client last got an ACK from the DHCP
	// server, and is meaningful only when HasLease is set. It is what makes a
	// freshly plugged-in device findable: its lease is the newest on the LAN.
	LeaseAge time.Duration
	HasLease bool
}

// Merge joins the DHCP server's view and the kernel's neighbour tables on the
// hardware address, which is the only identifier all three sources share.
//
// v4 and v6 are MAC -> addresses, as returned by Neighbours. leaseTime is the
// duration the DHCP server grants — see leaseAge for why it must be supplied.
// now is a parameter rather than a time.Now call so that tests are
// deterministic.
func Merge(leases []Lease, v4, v6 map[string][]string, leaseTime time.Duration, now time.Time) []Row {
	rows := map[string]*Row{}
	get := func(mac string) *Row {
		r, ok := rows[mac]
		if !ok {
			r = &Row{MAC: mac}
			rows[mac] = r
		}
		return r
	}

	// A reserved host also holds an active lease for the same address, so the
	// same MAC arrives twice. Keep the more specific source and the address
	// that came with it.
	for _, l := range leases {
		r := get(l.MAC)
		if l.Source.rank() > r.Source.rank() {
			r.Source = l.Source
			if l.IPv4 != "" {
				r.IPv4 = l.IPv4
			}
		} else if r.IPv4 == "" {
			r.IPv4 = l.IPv4
		}
		// The dynamic lease often carries a hostname the reservation does
		// not, and vice versa; take whichever turns up first.
		if r.Hostname == "" {
			r.Hostname = l.Hostname
		}
		// Only the active lease has an expiry; the reservation for the same
		// MAC does not, so this never overwrites a real value with a zero one.
		if !l.Expiry.IsZero() {
			r.LeaseAge = leaseAge(l.Expiry, leaseTime, now)
			r.HasLease = true
		}
	}

	for mac, addrs := range v4 {
		r := get(mac)
		if r.Source == "" {
			r.Source = SourceNeighbor
		}
		if r.IPv4 == "" {
			if picked := sortAddrs(addrs); len(picked) > 0 {
				r.IPv4 = picked[0]
			}
		}
	}

	for mac, addrs := range v6 {
		r := get(mac)
		if r.Source == "" {
			r.Source = SourceNeighbor
		}
		r.IPv6 = pickIPv6(addrs)
	}

	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// leaseAge works backwards from the expiry, because networkctl reports when a
// lease ends but not how long it was granted for — so the granted duration has
// to come from the caller, which reads it from the DHCP server's own config.
//
// If a client ever negotiates a shorter lease than the server's default, its
// age is overstated by the difference. Nothing on this LAN does: every observed
// lease sits in the 12h-24h band that a uniform 24h grant produces. Clamped at
// zero so clock skew cannot render a negative age.
func leaseAge(expiry time.Time, leaseTime time.Duration, now time.Time) time.Duration {
	age := leaseTime - expiry.Sub(now)
	if age < 0 {
		return 0
	}
	return age
}

// less orders by IPv4 numerically — the order someone scanning a /16 expects —
// with address-less rows collected at the bottom.
func less(a, b Row) bool {
	ai, aerr := netip.ParseAddr(a.IPv4)
	bi, berr := netip.ParseAddr(b.IPv4)
	switch {
	case aerr == nil && berr == nil:
		if ai != bi {
			return ai.Less(bi)
		}
	case aerr == nil:
		return true
	case berr == nil:
		return false
	}
	return a.MAC < b.MAC
}

// pickIPv6 reduces a host's neighbour entries to the addresses worth showing.
// Every SLAAC host also has a link-local address, which is noise next to a
// global one, so link-locals only survive when there is nothing else.
func pickIPv6(addrs []string) []string {
	var global, linkLocal []string
	for _, a := range dedupe(addrs) {
		addr, err := netip.ParseAddr(a)
		if err != nil {
			continue
		}
		if addr.IsLinkLocalUnicast() {
			linkLocal = append(linkLocal, a)
		} else {
			global = append(global, a)
		}
	}
	if len(global) > 0 {
		return sortAddrs(global)
	}
	return sortAddrs(linkLocal)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// sortAddrs sorts numerically, falling back to string order for anything that
// does not parse so the result is still deterministic.
func sortAddrs(in []string) []string {
	out := append([]string(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		a, aerr := netip.ParseAddr(out[i])
		b, berr := netip.ParseAddr(out[j])
		if aerr != nil || berr != nil {
			return out[i] < out[j]
		}
		return a.Less(b)
	})
	return out
}
