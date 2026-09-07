package collect

import (
	"reflect"
	"testing"
)

func TestParseNeighboursFixtures(t *testing.T) {
	v6, err := ParseNeighbours(readFixture(t, "neigh6.json"))
	if err != nil {
		t.Fatalf("ParseNeighbours(v6): %v", err)
	}
	// fus answers NDP for its own address and for Cilium's load-balancer
	// pool, so one MAC legitimately maps to several addresses.
	got := v6["78:55:36:00:4c:c4"]
	if len(got) < 2 {
		t.Errorf("fus IPv6 addresses = %v, want at least 2", got)
	}

	v4, err := ParseNeighbours(readFixture(t, "neigh4.json"))
	if err != nil {
		t.Fatalf("ParseNeighbours(v4): %v", err)
	}
	if len(v4) == 0 {
		t.Fatal("no IPv4 neighbours parsed")
	}
	for mac, addrs := range v4 {
		if mac == "" || len(addrs) == 0 {
			t.Errorf("bad entry %q -> %v", mac, addrs)
		}
	}
}

// Unreachable entries are printed without an lladdr; they are probe failures,
// not devices.
func TestParseNeighboursSkipsEntriesWithoutMAC(t *testing.T) {
	data := []byte(`[{"dst":"10.28.100.99","state":["FAILED"]},
	                 {"dst":"10.28.100.13","lladdr":"3C:31:74:E8:80:3B","state":["REACHABLE"]}]`)
	got, err := ParseNeighbours(data)
	if err != nil {
		t.Fatalf("ParseNeighbours: %v", err)
	}
	want := map[string][]string{"3c:31:74:e8:80:3b": {"10.28.100.13"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseNeighboursEmpty(t *testing.T) {
	// `ip -j neigh` prints nothing at all for an empty table.
	got, err := ParseNeighbours([]byte("\n"))
	if err != nil {
		t.Fatalf("ParseNeighbours: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
