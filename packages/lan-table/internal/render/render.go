// Package render turns an Inventory into the HTML page.
package render

import (
	_ "embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"net/netip"
	"strings"
	"time"

	"github.com/ngarvey/lan-table/internal/collect"
)

//go:embed templates/index.html
var indexHTML string

var index = template.Must(template.New("index").Funcs(template.FuncMap{
	"addrKey": addrKey,
	"textKey": textKey,
	"ageKey":  ageKey,
	"first":   first,
	"age":     age,
	"at":      at,
}).Parse(indexHTML))

// The sort keys below are compared as plain strings by the page's click
// handler, so the ordering has to be baked into the key itself. This tilde
// sorts after every character a key can otherwise start with, which parks
// empty cells at the bottom.
const emptyKey = "~"

// addrKey renders an address as its 16 raw bytes in hex, so that string
// comparison is numeric comparison — 10.28.100.9 before 10.28.100.10, which
// is exactly what lexicographic sorting of the printed form gets wrong.
func addrKey(s string) string {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		if s == "" {
			return emptyKey
		}
		return emptyKey + s
	}
	b := addr.As16()
	return hex.EncodeToString(b[:])
}

func textKey(s string) string {
	if s == "" {
		return emptyKey
	}
	return strings.ToLower(s)
}

func first(in []string) string {
	if len(in) == 0 {
		return ""
	}
	return in[0]
}

const timeFormat = "2006-01-02 15:04:05 MST"

// at turns a relative age back into a wall-clock time, for the tooltip that
// backs up the relative one shown in the cell.
func at(base time.Time, d time.Duration) string {
	return base.Add(-d).Format(timeFormat)
}

// ageKey is the age in seconds, zero-padded so that string comparison stays
// numeric — 9h must sort after 18m, which "32400" vs "1080" would get wrong.
// Ten digits covers every duration this page can produce.
func ageKey(d time.Duration) string {
	return fmt.Sprintf("%010d", int64(d.Seconds()))
}

// age renders a duration the way you read one at a glance: the largest unit
// that still says something, and never more than two.
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		if m := int(d.Minutes()) % 60; m > 0 {
			return fmt.Sprintf("%dh %dm ago", h, m)
		}
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours()) / 24
		if h := int(d.Hours()) % 24; h > 0 {
			return fmt.Sprintf("%dd %dh ago", days, h)
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

type page struct {
	Rows        []collect.Row
	Interface   string
	Generated   string
	GeneratedAt time.Time
}

// Page writes the device table. `now` is passed in so tests get a fixed
// timestamp.
func Page(w io.Writer, inv collect.Inventory, now time.Time) error {
	return index.Execute(w, page{
		Rows:        inv.Rows,
		Interface:   inv.Interface,
		Generated:   now.Format(timeFormat),
		GeneratedAt: now,
	})
}
