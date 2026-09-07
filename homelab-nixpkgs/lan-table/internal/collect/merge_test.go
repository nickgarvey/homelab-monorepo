package collect

import (
	"reflect"
	"testing"
	"time"
)

var (
	testNow   = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	testLease = 24 * time.Hour
)

func TestMergeStaticBeatsDynamicLease(t *testing.T) {
	// A reserved host holds an active lease too, so the same MAC arrives from
	// both StaticLeases and Leases.
	leases := []Lease{
		{MAC: "aa:bb:cc:dd:ee:01", IPv4: "10.28.1.5", Hostname: "nelkir", Source: SourceStatic},
		{MAC: "aa:bb:cc:dd:ee:01", IPv4: "10.28.1.5", Hostname: "nelkir", Source: SourceDHCP},
	}
	rows := Merge(leases, nil, nil, testLease, testNow)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].Source != SourceStatic {
		t.Errorf("source = %q, want %q", rows[0].Source, SourceStatic)
	}
}

// The reservation carries no hostname but the client announces one; the row
// should end up with it.
func TestMergeTakesHostnameFromEitherLease(t *testing.T) {
	leases := []Lease{
		{MAC: "aa:bb:cc:dd:ee:01", IPv4: "10.28.4.2", Source: SourceStatic},
		{MAC: "aa:bb:cc:dd:ee:01", IPv4: "10.28.4.2", Hostname: "camera", Source: SourceDHCP},
	}
	rows := Merge(leases, nil, nil, testLease, testNow)
	if rows[0].Hostname != "camera" {
		t.Errorf("hostname = %q, want %q", rows[0].Hostname, "camera")
	}
}

func TestMergeNeighbourOnlyDevice(t *testing.T) {
	v4 := map[string][]string{"aa:bb:cc:dd:ee:02": {"10.28.0.77"}}
	v6 := map[string][]string{"aa:bb:cc:dd:ee:02": {"2001:470:482f::99"}}
	rows := Merge(nil, v4, v6, testLease, testNow)
	want := []Row{{
		MAC:    "aa:bb:cc:dd:ee:02",
		IPv4:   "10.28.0.77",
		IPv6:   []string{"2001:470:482f::99"},
		Source: SourceNeighbor,
	}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("got %+v, want %+v", rows, want)
	}
}

// A leased device keeps its lease address even though the neighbour table also
// knows one, and keeps its lease-derived source.
func TestMergeLeaseAddressWinsOverNeighbour(t *testing.T) {
	leases := []Lease{{MAC: "aa:bb:cc:dd:ee:03", IPv4: "10.28.1.4", Source: SourceStatic}}
	v4 := map[string][]string{"aa:bb:cc:dd:ee:03": {"10.28.99.99"}}
	rows := Merge(leases, v4, nil, testLease, testNow)
	if rows[0].IPv4 != "10.28.1.4" || rows[0].Source != SourceStatic {
		t.Errorf("got %+v, want 10.28.1.4/static", rows[0])
	}
}

func TestMergeSortsByIPv4WithAddresslessLast(t *testing.T) {
	leases := []Lease{
		{MAC: "aa:bb:cc:dd:ee:11", IPv4: "10.28.100.9", Source: SourceDHCP},
		{MAC: "aa:bb:cc:dd:ee:12", IPv4: "10.28.12.108", Source: SourceStatic},
		{MAC: "aa:bb:cc:dd:ee:13", IPv4: "10.28.100.10", Source: SourceDHCP},
	}
	v6 := map[string][]string{"aa:bb:cc:dd:ee:14": {"2001:470:482f::7"}}
	rows := Merge(leases, nil, v6, testLease, testNow)

	var order []string
	for _, r := range rows {
		order = append(order, r.IPv4)
	}
	// 10.28.100.9 before .10 proves the sort is numeric, not lexicographic.
	want := []string{"10.28.12.108", "10.28.100.9", "10.28.100.10", ""}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestPickIPv6(t *testing.T) {
	tests := []struct {
		name  string
		addrs []string
		want  []string
	}{
		{
			// Every SLAAC host has a link-local; it is noise beside a global.
			name:  "link-local dropped when a global exists",
			addrs: []string{"fe80::7a55:36ff:fe00:4cc4", "2001:470:482f::21"},
			want:  []string{"2001:470:482f::21"},
		},
		{
			name:  "link-local kept when it is all there is",
			addrs: []string{"fe80::12bc:2692:2c0d:e0b2"},
			want:  []string{"fe80::12bc:2692:2c0d:e0b2"},
		},
		{
			name:  "globals deduped and sorted numerically",
			addrs: []string{"2001:470:482f:2::5300", "2001:470:482f::21", "2001:470:482f:2::5300"},
			want:  []string{"2001:470:482f::21", "2001:470:482f:2::5300"},
		},
		{
			name:  "unparseable addresses dropped",
			addrs: []string{"not-an-address"},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickIPv6(tt.addrs)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("pickIPv6(%v) = %v, want %v", tt.addrs, got, tt.want)
			}
		})
	}
}

// End-to-end over the captured router state: every row must have a MAC and a
// source, and the known reservations must come through as static.
func TestMergeAgainstFixtures(t *testing.T) {
	leases, err := ParseLeases(readFixture(t, "networkctl.json"))
	if err != nil {
		t.Fatal(err)
	}
	v4, err := ParseNeighbours(readFixture(t, "neigh4.json"))
	if err != nil {
		t.Fatal(err)
	}
	v6, err := ParseNeighbours(readFixture(t, "neigh6.json"))
	if err != nil {
		t.Fatal(err)
	}

	rows := Merge(leases, v4, v6, testLease, testNow)
	if len(rows) < len(leases) {
		t.Errorf("got %d rows from %d leases; merge lost devices", len(rows), len(leases))
	}
	byMAC := map[string]Row{}
	for _, r := range rows {
		if r.MAC == "" || r.Source == "" {
			t.Errorf("row %+v is missing MAC or source", r)
		}
		byMAC[r.MAC] = r
	}

	// fus: a reservation from lan-hosts.nix that is also an active NDP
	// neighbour, so it exercises all three sources at once.
	fus := byMAC["78:55:36:00:4c:c4"]
	if fus.Source != SourceStatic || fus.IPv4 != "10.28.15.1" || fus.Hostname != "fus" {
		t.Errorf("fus = %+v, want static 10.28.15.1", fus)
	}
	if len(fus.IPv6) == 0 {
		t.Error("fus has no IPv6 address; the NDP join did not happen")
	}
	for _, a := range fus.IPv6 {
		if len(a) >= 4 && a[:4] == "fe80" {
			t.Errorf("fus IPv6 %s is link-local but a global exists", a)
		}
	}
}

func TestMergeLeaseAge(t *testing.T) {
	tests := []struct {
		name   string
		expiry time.Time
		want   time.Duration
	}{
		{
			// The whole point: a client that ACKed minutes ago has almost the
			// full lease left, so it has the smallest age on the LAN.
			name:   "just acked",
			expiry: testNow.Add(23*time.Hour + 40*time.Minute),
			want:   20 * time.Minute,
		},
		{
			// A renewal at T1 leaves a full lease again; halfway through is
			// the oldest a lease gets before the client comes back.
			name:   "due to renew",
			expiry: testNow.Add(12 * time.Hour),
			want:   12 * time.Hour,
		},
		{
			// Clock skew must not produce a negative age.
			name:   "expiry beyond the lease time",
			expiry: testNow.Add(25 * time.Hour),
			want:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := Merge([]Lease{{
				MAC: "aa:bb:cc:dd:ee:04", IPv4: "10.28.100.7",
				Source: SourceDHCP, Expiry: tt.expiry,
			}}, nil, nil, testLease, testNow)
			if !rows[0].HasLease {
				t.Fatal("HasLease is false for a lease with an expiry")
			}
			if rows[0].LeaseAge != tt.want {
				t.Errorf("LeaseAge = %v, want %v", rows[0].LeaseAge, tt.want)
			}
		})
	}
}

// A reservation has no expiry of its own. It must not clear the expiry that
// arrived with the same client's active lease, whichever order they merge in.
func TestMergeReservationDoesNotClearLeaseAge(t *testing.T) {
	leases := []Lease{
		{MAC: "aa:bb:cc:dd:ee:05", IPv4: "10.28.1.5", Source: SourceStatic},
		{MAC: "aa:bb:cc:dd:ee:05", IPv4: "10.28.1.5", Source: SourceDHCP,
			Expiry: testNow.Add(23 * time.Hour)},
	}
	rows := Merge(leases, nil, nil, testLease, testNow)
	if !rows[0].HasLease || rows[0].LeaseAge != time.Hour {
		t.Errorf("LeaseAge = %v (has=%v), want 1h", rows[0].LeaseAge, rows[0].HasLease)
	}
}

// A device known only from the neighbour table has no lease at all, and must
// not render as though it were leased a moment ago.
func TestMergeNeighbourOnlyHasNoLease(t *testing.T) {
	rows := Merge(nil, map[string][]string{"aa:bb:cc:dd:ee:06": {"10.28.0.77"}}, nil, testLease, testNow)
	if rows[0].HasLease {
		t.Errorf("HasLease is true for a neighbour-only device: %+v", rows[0])
	}
}

// Every active lease in the captured router state should yield an age inside
// the 12h-24h band that a uniform 24h grant produces.
func TestMergeLeaseAgeAgainstFixture(t *testing.T) {
	leases, err := ParseLeases(readFixture(t, "networkctl.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The fixture was captured at a fixed wall-clock moment; anchor `now` to
	// the newest expiry so the ages are the ones seen at capture time.
	var newest time.Time
	for _, l := range leases {
		if l.Expiry.After(newest) {
			newest = l.Expiry
		}
	}
	if newest.IsZero() {
		t.Fatal("no lease in the fixture carried an expiry")
	}
	now := newest.Add(-testLease)

	var leased int
	for _, r := range Merge(leases, nil, nil, testLease, now) {
		if !r.HasLease {
			continue
		}
		leased++
		if r.LeaseAge < 0 || r.LeaseAge > testLease {
			t.Errorf("%s lease age %v is outside the lease time", r.MAC, r.LeaseAge)
		}
	}
	if leased == 0 {
		t.Fatal("no rows came out with a lease")
	}
}
