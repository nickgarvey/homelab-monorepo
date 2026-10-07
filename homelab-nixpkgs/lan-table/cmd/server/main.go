// lan-table serves a single page listing every device on the LAN, joining the
// router's DHCP reservations, its active leases, and its ARP/NDP neighbour
// tables. It is read-only and holds no state.
package main

import (
	"bytes"
	"context"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/ngarvey/lan-table/internal/collect"
	"github.com/ngarvey/lan-table/internal/render"
)

func main() {
	listen := flag.String("listen", ":8080", "address to listen on")
	iface := flag.String("interface", "br-lan", "LAN interface to report on")
	// networkctl reports when a lease expires but not how long it was granted
	// for, so lease age can only be worked out if the granted duration is
	// supplied. The NixOS module wires this from the DHCP server's own config.
	leaseTime := flag.Duration("lease-time", 24*time.Hour, "lease duration the DHCP server grants")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n")) //nolint:errcheck
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handle(w, r, *iface, *leaseTime)
	})

	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("lan-table listening on %s, reporting on %s", *listen, *iface)
	log.Fatal(srv.ListenAndServe())
}

// handle collects on every request. Both underlying commands are cheap and
// the data is only interesting when it is current, so there is no cache to go
// stale.
func handle(w http.ResponseWriter, r *http.Request, iface string, leaseTime time.Duration) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	inv, err := collect.Collect(ctx, iface, leaseTime)
	if err != nil {
		log.Printf("collect failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Render to a buffer first so a template failure still produces a 500
	// rather than a half-written 200.
	var buf bytes.Buffer
	if err := render.Page(&buf, inv, time.Now()); err != nil {
		log.Printf("render failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w) //nolint:errcheck
}
