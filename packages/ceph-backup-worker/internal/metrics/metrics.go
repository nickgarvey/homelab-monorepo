// Package metrics exports the Ceph backup's state to Prometheus, computed
// from the Kopia repository rather than from runs, so a run that
// "succeeded" without writing a snapshot still alerts. Only the pod holding
// the ceph-backup-metrics Lease computes them; the others export none of
// these series. Names and labels are those of the bash job's, which the
// rules in k8s/manifests/prometheus/rules/ceph-backup.yaml use.
package metrics

import (
	"context"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"homelab/ceph-backup-worker/internal/backup"
)

// Source is where the metrics come from.
type Source interface {
	// Inventory is what exists in Ceph now.
	Inventory(ctx context.Context) (backup.Inventory, error)
	// Snapshots is every snapshot in the repository.
	Snapshots(ctx context.Context) ([]backup.Snapshot, error)
	// StorjSnapshots is every snapshot in the Storj copy.
	StorjSnapshots(ctx context.Context) ([]backup.Snapshot, error)
	// LastRun is the newest finished CephBackup run: whether it succeeded
	// and when it closed; found is false if there is none.
	LastRun(ctx context.Context) (ok bool, at time.Time, found bool, err error)
}

// Collector holds the gauges.
type Collector struct {
	item        *prometheus.GaugeVec
	lastSuccess *prometheus.GaugeVec
	// Without labels, but vectors: a gauge exports 0 before it is set,
	// which would read as a failed run or a copy from 1970. These export
	// nothing until there is a value.
	runSuccess *prometheus.GaugeVec
	runTime    *prometheus.GaugeVec
	storjSync  *prometheus.GaugeVec
	reg        prometheus.Registerer
	// lead is held by Lead, so a Lead still winding down after a lost
	// Lease finishes before the next starts.
	lead sync.Mutex
}

// New makes the gauges; they are registered with reg while this pod leads.
func New(reg prometheus.Registerer) *Collector {
	return &Collector{
		reg: reg,
		item: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_ceph_backup_item",
			Help: "Something in Ceph that should be backed up (kind=pool: found but not backed up).",
		}, []string{"kind", "name"}),
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_ceph_backup_last_success_timestamp_seconds",
			Help: "End time of the newest snapshot without errors of the item, from the Kopia repository.",
		}, []string{"kind", "name"}),
		runSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_ceph_backup_last_run_success",
			Help: "1 if the newest finished CephBackup run succeeded.",
		}, nil),
		runTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_ceph_backup_last_run_timestamp_seconds",
			Help: "When the newest finished CephBackup run closed.",
		}, nil),
		storjSync: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "homelab_ceph_backup_storj_last_sync_timestamp_seconds",
			Help: "End time of the newest snapshot in the Storj copy.",
		}, nil),
	}
}

func (c *Collector) collectors() []prometheus.Collector {
	return []prometheus.Collector{c.item, c.lastSuccess, c.runSuccess, c.runTime, c.storjSync}
}

// Lead computes the metrics every interval until ctx ends, then withdraws
// them. Run it while holding the Lease.
func (c *Collector) Lead(ctx context.Context, src Source, interval time.Duration) {
	c.lead.Lock()
	defer c.lead.Unlock()
	c.Update(ctx, src)
	for _, m := range c.collectors() {
		if err := c.reg.Register(m); err != nil {
			slog.Error("registering metric", "error", err)
		}
	}
	defer func() {
		for _, m := range c.collectors() {
			c.reg.Unregister(m)
		}
	}()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Update(ctx, src)
		}
	}
}

// Update recomputes the metrics. A part whose source fails keeps its
// previous values, so a timestamp only grows stale and the staleness alerts
// still fire.
func (c *Collector) Update(ctx context.Context, src Source) {
	inv, err := src.Inventory(ctx)
	if err != nil {
		slog.Error("metrics: inventory", "error", err)
	}
	snaps, serr := src.Snapshots(ctx)
	if serr != nil {
		slog.Error("metrics: snapshots", "error", serr)
	}
	if err == nil && serr == nil {
		c.item.Reset()
		c.lastSuccess.Reset()
		last := LastSuccess(snaps)
		for _, it := range inv.Items {
			c.item.WithLabelValues(it.Kind, it.Name).Set(1)
			if t, ok := last[Key{it.Kind, it.Name}]; ok && it.Kind != backup.KindPool {
				c.lastSuccess.WithLabelValues(it.Kind, it.Name).Set(float64(t.Unix()))
			}
		}
	}

	if ok, at, found, err := src.LastRun(ctx); err != nil {
		slog.Error("metrics: last run", "error", err)
	} else if found {
		c.runTime.WithLabelValues().Set(float64(at.Unix()))
		if ok {
			c.runSuccess.WithLabelValues().Set(1)
		} else {
			c.runSuccess.WithLabelValues().Set(0)
		}
	}

	if storj, err := src.StorjSnapshots(ctx); err != nil {
		slog.Error("metrics: Storj snapshots", "error", err)
	} else {
		var newest time.Time
		for _, s := range storj {
			if s.EndTime.After(newest) {
				newest = s.EndTime
			}
		}
		if !newest.IsZero() {
			c.storjSync.WithLabelValues().Set(float64(newest.Unix()))
		}
	}
}

// Key is an item's metric labels.
type Key struct{ Kind, Name string }

// LastSuccess is the end time of the newest snapshot without errors of each
// item, by its labels.
func LastSuccess(snaps []backup.Snapshot) map[Key]time.Time {
	out := map[Key]time.Time{}
	for _, s := range snaps {
		if s.Source.Host != "ceph-backup" || s.Source.UserName != "root" ||
			s.Stats.ErrorCount > 0 || s.Stats.IgnoredErrorCount > 0 {
			continue
		}
		k, ok := ItemOf(s.Source.Path)
		if !ok {
			continue
		}
		if s.EndTime.After(out[k]) {
			out[k] = s.EndTime
		}
	}
	return out
}

// ItemOf maps a Kopia source path to the item it backs up (see the paths in
// backup.Config).
func ItemOf(p string) (Key, bool) {
	p = path.Clean(p)
	rest, ok := strings.CutPrefix(p, "/run/")
	if !ok {
		return Key{}, false
	}
	parts := strings.Split(rest, "/")
	switch {
	case parts[0] == "ceph-backup" && len(parts) == 3:
		return Key{backup.KindRBD, parts[1] + "/" + parts[2]}, true
	case parts[0] == "ceph-backup-rbd" && len(parts) == 3:
		return Key{backup.KindRBD, parts[1] + "/" + parts[2]}, true
	case parts[0] == "ceph-backup-rgw" && len(parts) == 2:
		return Key{backup.KindRGW, parts[1]}, true
	case parts[0] == "ceph-backup-rgw-metadata" && len(parts) == 1:
		return Key{backup.KindRGWMetadata, "default"}, true
	case parts[0] == "ceph-backup-cephfs" && len(parts) == 2:
		return Key{backup.KindCephFS, parts[1]}, true
	}
	return Key{}, false
}
