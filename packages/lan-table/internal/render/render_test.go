package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ngarvey/lan-table/internal/collect"
)

func TestPage(t *testing.T) {
	inv := collect.Inventory{
		Interface: "br-lan",
		Rows: []collect.Row{
			{Hostname: "fus", IPv4: "10.28.15.1", IPv6: []string{"2001:470:482f::21"},
				Source: collect.SourceStatic, MAC: "78:55:36:00:4c:c4"},
			{IPv4: "10.28.100.19", Source: collect.SourceDHCP, MAC: "cc:a7:c1:62:5a:59"},
		},
	}
	var buf bytes.Buffer
	if err := Page(&buf, inv, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Page: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"fus", "10.28.15.1", "2001:470:482f::21", "static",
		"78:55:36:00:4c:c4", "cc:a7:c1:62:5a:59", "dhcp",
		"2 devices on br-lan", "2026-09-06 12:00:00 UTC",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
	// The row with no hostname and no IPv6 gets em-dashes, not blank cells.
	// One row lacks a hostname and an IPv6; neither row has a lease.
	if strings.Count(out, "&mdash;") != 4 {
		t.Errorf("want 4 em-dash placeholders, got %d", strings.Count(out, "&mdash;"))
	}
}

// A hostname is whatever the client announced, so it must be escaped.
func TestPageEscapesHostname(t *testing.T) {
	inv := collect.Inventory{Interface: "br-lan", Rows: []collect.Row{
		{Hostname: `<script>alert(1)</script>`, MAC: "aa:bb:cc:dd:ee:ff", Source: collect.SourceDHCP},
	}}
	var buf bytes.Buffer
	if err := Page(&buf, inv, time.Now()); err != nil {
		t.Fatalf("Page: %v", err)
	}
	out := buf.String()
	// The page has a <script> block of its own, so look for the payload.
	if strings.Contains(out, "<script>alert") || strings.Contains(out, "alert(1)</script>") {
		t.Error("hostname was not escaped")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("escaped hostname missing from the page")
	}
}

// The whole point of the server-side key: string comparison of the printed
// form puts 10.28.100.10 before 10.28.100.9, and the key must not.
func TestAddrKeyOrdersNumerically(t *testing.T) {
	tests := []struct {
		name   string
		lo, hi string
	}{
		{"ipv4 octet is numeric", "10.28.100.9", "10.28.100.10"},
		{"ipv4 across subnets", "10.28.12.108", "10.28.100.9"},
		{"ipv6 hextet is numeric", "2001:470:482f::9", "2001:470:482f::10"},
		{"empty sorts last", "255.255.255.255", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lo, hi := addrKey(tt.lo), addrKey(tt.hi)
			if !(lo < hi) {
				t.Errorf("addrKey(%q)=%q should sort before addrKey(%q)=%q", tt.lo, lo, tt.hi, hi)
			}
		})
	}
}

func TestTextKeyIsCaseInsensitiveAndEmptyLast(t *testing.T) {
	if textKey("LGwebOSTV") >= textKey("mac") {
		t.Error("textKey should compare case-insensitively")
	}
	if textKey("zzz") >= textKey("") {
		t.Error("empty text should sort last")
	}
}

func TestPageEmitsSortKeys(t *testing.T) {
	inv := collect.Inventory{Interface: "br-lan", Rows: []collect.Row{
		{Hostname: "fus", IPv4: "10.28.15.1", IPv6: []string{"2001:470:482f::21"},
			Source: collect.SourceStatic, MAC: "78:55:36:00:4c:c4"},
	}}
	var buf bytes.Buffer
	if err := Page(&buf, inv, time.Now()); err != nil {
		t.Fatalf("Page: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`data-sort="fus"`,
		`data-sort="` + addrKey("10.28.15.1") + `"`,
		`data-sort="` + addrKey("2001:470:482f::21") + `"`,
		`data-sort="static"`,
		`data-sort="78:55:36:00:4c:c4"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("page does not contain %s", want)
		}
	}
	// One key per column, on the single body row.
	if got := strings.Count(out, "data-sort="); got != 6 {
		t.Errorf("got %d sort keys, want 6", got)
	}
}

func TestAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{27 * time.Second, "27s ago"},
		{time.Minute, "1m ago"},
		{18 * time.Minute, "18m ago"},
		{time.Hour, "1h ago"},
		{90 * time.Minute, "1h 30m ago"},
		{23*time.Hour + 40*time.Minute, "23h 40m ago"},
		{24 * time.Hour, "1d ago"},
		{50 * time.Hour, "2d 2h ago"},
	}
	for _, tt := range tests {
		if got := age(tt.d); got != tt.want {
			t.Errorf("age(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// The Leased column sorts on this key as a string, so a longer duration must
// produce a greater key — which the unpadded seconds count would not.
func TestAgeKeyOrdersNumerically(t *testing.T) {
	if !(ageKey(18*time.Minute) < ageKey(9*time.Hour)) {
		t.Errorf("18m key %q should sort before 9h key %q",
			ageKey(18*time.Minute), ageKey(9*time.Hour))
	}
	if !(ageKey(0) < ageKey(time.Second)) {
		t.Error("zero should sort first")
	}
	// Valueless rows use the shared sentinel, which must sort after any age.
	if !(ageKey(9999*time.Hour) < emptyKey) {
		t.Error("every age should sort before the empty sentinel")
	}
}

func TestPageLeaseColumn(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	inv := collect.Inventory{Interface: "br-lan", Rows: []collect.Row{
		{IPv4: "10.28.100.202", MAC: "aa:bb:cc:dd:ee:01", Source: collect.SourceDHCP,
			LeaseAge: 20 * time.Minute, HasLease: true},
		{IPv4: "10.28.0.77", MAC: "aa:bb:cc:dd:ee:02", Source: collect.SourceNeighbor},
	}}
	var buf bytes.Buffer
	if err := Page(&buf, inv, now); err != nil {
		t.Fatalf("Page: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "20m ago") {
		t.Error("leased cell missing its relative age")
	}
	// The tooltip resolves the relative age to a wall-clock time.
	if !strings.Contains(out, "2026-09-07 11:40:00 UTC") {
		t.Error("leased cell missing its absolute-time tooltip")
	}
	if !strings.Contains(out, `data-sort="`+ageKey(20*time.Minute)+`"`) {
		t.Error("leased cell missing its sort key")
	}
	// The row without a lease gets the sentinel, not a zero age.
	if strings.Contains(out, "0s ago") {
		t.Error("a row with no lease rendered as freshly leased")
	}
	// Six columns now, so six keys per row.
	if got := strings.Count(out, "data-sort="); got != 12 {
		t.Errorf("got %d sort keys for 2 rows, want 12", got)
	}
	// The badge logic keys off this attribute.
	for _, r := range inv.Rows {
		if !strings.Contains(out, `data-mac="`+r.MAC+`"`) {
			t.Errorf("row for %s has no data-mac attribute", r.MAC)
		}
	}
}
