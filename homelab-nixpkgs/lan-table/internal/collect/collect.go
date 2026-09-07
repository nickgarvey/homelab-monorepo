// Package collect gathers the LAN inventory from the router's own runtime
// state. Nothing here is privileged: the networkd varlink socket is
// world-connectable and the neighbour tables are readable by anyone, so the
// service can run as a DynamicUser.
package collect

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Source records where a row's identity came from. The values are also what
// the rendered table shows, so they are lowercase words rather than an enum
// the template would have to translate.
type Source string

const (
	// SourceStatic is a reservation configured on the DHCP server. On this
	// router those are generated from modules/networking/lan-hosts.nix.
	SourceStatic Source = "static"
	// SourceDHCP is an active lease from the dynamic pool.
	SourceDHCP Source = "dhcp"
	// SourceNeighbor is a device that never took a lease and is known only
	// because the router has ARP/NDP state for it — a hand-configured
	// address, most often.
	SourceNeighbor Source = "neighbor"
)

// rank orders sources by how much they tell us about a device, so a MAC seen
// in more than one place keeps the most specific label.
func (s Source) rank() int {
	switch s {
	case SourceStatic:
		return 3
	case SourceDHCP:
		return 2
	default:
		return 1
	}
}

// Inventory is everything the collectors found, already merged.
type Inventory struct {
	Rows      []Row
	Interface string
}

// run executes a command and returns stdout. Stderr is folded into the error
// because both `networkctl` and `ip` explain themselves there and the message
// is the only diagnostic the HTTP handler can surface.
func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err,
			strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Collect gathers and merges every source for one LAN interface. leaseTime is
// the duration the DHCP server grants, which networkctl does not report.
func Collect(ctx context.Context, iface string, leaseTime time.Duration) (Inventory, error) {
	leases, err := Leases(ctx, iface)
	if err != nil {
		return Inventory{}, err
	}
	v6, err := Neighbours(ctx, iface, 6)
	if err != nil {
		return Inventory{}, err
	}
	v4, err := Neighbours(ctx, iface, 4)
	if err != nil {
		return Inventory{}, err
	}
	return Inventory{
		Rows:      Merge(leases, v4, v6, leaseTime, time.Now()),
		Interface: iface,
	}, nil
}
