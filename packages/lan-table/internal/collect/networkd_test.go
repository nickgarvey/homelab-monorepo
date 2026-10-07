package collect

import (
	"os"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return data
}

// The fixture is real `networkctl --json=short status br-lan` output from the
// router, so this covers the shapes that actually occur: static leases with an
// empty HardwareAddress, active leases with one, and a DUID-style ClientId.
func TestParseLeasesFixture(t *testing.T) {
	leases, err := ParseLeases(readFixture(t, "networkctl.json"))
	if err != nil {
		t.Fatalf("ParseLeases: %v", err)
	}

	var static, dhcp int
	byMAC := map[string]Lease{}
	for _, l := range leases {
		if l.MAC == "" {
			t.Errorf("lease %+v has no MAC", l)
		}
		if l.Source == SourceStatic {
			static++
		} else {
			dhcp++
		}
		if _, ok := byMAC[l.MAC]; !ok || l.Source == SourceStatic {
			byMAC[l.MAC] = l
		}
	}
	if static == 0 || dhcp == 0 {
		t.Fatalf("want both static and dynamic leases, got static=%d dhcp=%d", static, dhcp)
	}

	// A reservation from lan-hosts.nix: MAC recovered from ClientId.
	want := Lease{MAC: "78:55:36:00:4d:80", IPv4: "10.28.15.3", Hostname: "dah", Source: SourceStatic}
	if got := byMAC["78:55:36:00:4d:80"]; got != want {
		t.Errorf("dah reservation = %+v, want %+v", got, want)
	}

	// Static leases must be listed before dynamic ones so Merge's precedence
	// does not depend on ordering luck.
	seenDynamic := false
	for _, l := range leases {
		if l.Source == SourceDHCP {
			seenDynamic = true
		} else if seenDynamic {
			t.Fatal("static lease appears after a dynamic one")
		}
	}
}

func TestParseLeasesInterfacesWrapper(t *testing.T) {
	data := []byte(`{"Interfaces":[{"DHCPServer":{"Leases":[
	  {"AddressString":"10.28.100.7","Hostname":"Mac","ClientId":[1,246,143,136,234,172,229],
	   "HardwareAddress":[246,143,136,234,172,229]}]}}]}`)
	leases, err := ParseLeases(data)
	if err != nil {
		t.Fatalf("ParseLeases: %v", err)
	}
	want := []Lease{{MAC: "f6:8f:88:ea:ac:e5", IPv4: "10.28.100.7", Hostname: "Mac", Source: SourceDHCP}}
	if len(leases) != 1 || leases[0] != want[0] {
		t.Errorf("got %+v, want %+v", leases, want)
	}
}

func TestParseLeasesNoServer(t *testing.T) {
	leases, err := ParseLeases([]byte(`{"Index":3,"Name":"enp4s0"}`))
	if err != nil {
		t.Fatalf("ParseLeases: %v", err)
	}
	if len(leases) != 0 {
		t.Errorf("got %d leases for an interface with no DHCP server, want 0", len(leases))
	}
}

func TestLeaseMAC(t *testing.T) {
	tests := []struct {
		name  string
		lease dhcpLease
		want  string
	}{
		{"hardware address wins", dhcpLease{
			HardwareAddress: []int{0x6c, 0x55, 0xb1, 0xca, 0x13, 0x1c},
			ClientID:        []int{255, 177, 202, 19, 28, 0, 1, 0, 1},
		}, "6c:55:b1:ca:13:1c"},
		{"ethernet client id", dhcpLease{
			ClientID: []int{1, 0x9c, 0x6b, 0x00, 0xaf, 0xe9, 0xd0},
		}, "9c:6b:00:af:e9:d0"},
		{"duid client id, no hardware address", dhcpLease{
			ClientID: []int{255, 177, 202, 19, 28, 0, 1, 0, 1},
		}, ""},
		{"empty", dhcpLease{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := leaseMAC(tt.lease); got != tt.want {
				t.Errorf("leaseMAC() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Active leases carry an expiry; reservations do not, because a reservation is
// configuration rather than a granted lease.
func TestParseLeasesExpiry(t *testing.T) {
	leases, err := ParseLeases(readFixture(t, "networkctl.json"))
	if err != nil {
		t.Fatal(err)
	}
	var withExpiry, statics int
	for _, l := range leases {
		if l.Source == SourceStatic {
			statics++
			if !l.Expiry.IsZero() {
				t.Errorf("reservation %s has an expiry %v", l.MAC, l.Expiry)
			}
			continue
		}
		if l.Expiry.IsZero() {
			t.Errorf("active lease %s has no expiry", l.MAC)
			continue
		}
		withExpiry++
	}
	if withExpiry == 0 || statics == 0 {
		t.Fatalf("want both kinds in the fixture, got %d active and %d static", withExpiry, statics)
	}
}
