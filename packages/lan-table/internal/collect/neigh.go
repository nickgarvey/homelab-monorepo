package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type neighEntry struct {
	Dst    string `json:"dst"`
	LLAddr string `json:"lladdr"`
}

// Neighbours returns MAC -> addresses from the kernel's ARP/NDP table for one
// address family. This is the only source of IPv6 on this LAN: addressing is
// RA/SLAAC with no DHCPv6 server, so the router never hands out a v6 address
// and cannot know one except by having talked to the host.
func Neighbours(ctx context.Context, iface string, family int) (map[string][]string, error) {
	out, err := run(ctx, "ip", "-j", "-"+strconv.Itoa(family), "neigh", "show", "dev", iface)
	if err != nil {
		return nil, err
	}
	return ParseNeighbours(out)
}

// ParseNeighbours decodes `ip -j neigh` output. Entries in FAILED or
// INCOMPLETE state have no lladdr — they are unanswered probes, not devices —
// and are dropped.
func ParseNeighbours(data []byte) (map[string][]string, error) {
	// An empty table prints nothing at all rather than "[]".
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string][]string{}, nil
	}
	var entries []neighEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decoding ip neigh output: %w", err)
	}

	byMAC := map[string][]string{}
	for _, e := range entries {
		if e.LLAddr == "" || e.Dst == "" {
			continue
		}
		mac := strings.ToLower(e.LLAddr)
		byMAC[mac] = append(byMAC[mac], e.Dst)
	}
	return byMAC, nil
}
