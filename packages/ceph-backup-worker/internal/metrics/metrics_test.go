package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"homelab/ceph-backup-worker/internal/backup"
)

func TestItemOf(t *testing.T) {
	for path, want := range map[string]Key{
		"/run/ceph-backup/media/jellyfin-config": {backup.KindRBD, "media/jellyfin-config"},
		"/run/ceph-backup-rbd/kubernetes/plain":  {backup.KindRBD, "kubernetes/plain"},
		"/run/ceph-backup-rgw/photos":            {backup.KindRGW, "photos"},
		"/run/ceph-backup-rgw-metadata":          {backup.KindRGWMetadata, "default"},
		"/run/ceph-backup-cephfs/local-drive":    {backup.KindCephFS, "local-drive"},
	} {
		if got, ok := ItemOf(path); !ok || got != want {
			t.Errorf("%s: %v %v, want %v", path, got, ok, want)
		}
	}
	for _, path := range []string{"/run/ceph-backup/media", "/var/x", "/run/ceph-backup-cephfs-live/x", "/run/ceph-restore/a/b"} {
		if k, ok := ItemOf(path); ok {
			t.Errorf("%s: %v", path, k)
		}
	}
}

func snap(path string, end time.Time, errs int64) backup.Snapshot {
	var s backup.Snapshot
	s.Source.Host, s.Source.UserName, s.Source.Path = "ceph-backup", "root", path
	s.EndTime = end
	s.Stats.ErrorCount = errs
	return s
}

type fakeSource struct {
	inv      backup.Inventory
	snaps    []backup.Snapshot
	storj    []backup.Snapshot
	invErr   error
	runOK    bool
	runAt    time.Time
	runFound bool
}

func (f *fakeSource) Inventory(context.Context) (backup.Inventory, error)  { return f.inv, f.invErr }
func (f *fakeSource) Snapshots(context.Context) ([]backup.Snapshot, error) { return f.snaps, nil }
func (f *fakeSource) StorjSnapshots(context.Context) ([]backup.Snapshot, error) {
	return f.storj, nil
}
func (f *fakeSource) LastRun(context.Context) (bool, time.Time, bool, error) {
	return f.runOK, f.runAt, f.runFound, nil
}

func TestUpdate(t *testing.T) {
	t1 := time.Unix(1_800_000_000, 0)
	t2 := t1.Add(24 * time.Hour)
	src := &fakeSource{
		inv: backup.Inventory{Items: []backup.Item{
			{Kind: backup.KindRBD, Name: "media/jellyfin-config"},
			{Kind: backup.KindRGW, Name: "photos"},
			{Kind: backup.KindRGW, Name: "never"},
			{Kind: backup.KindPool, Name: "mystery"},
		}},
		snaps: []backup.Snapshot{
			snap("/run/ceph-backup/media/jellyfin-config", t1, 0),
			snap("/run/ceph-backup/media/jellyfin-config", t2, 0),
			snap("/run/ceph-backup-rgw/photos", t1, 0),
			// With errors: not a success.
			snap("/run/ceph-backup-rgw/photos", t2, 3),
			// Deleted since: not exported.
			snap("/run/ceph-backup/old/gone", t2, 0),
		},
		storj:    []backup.Snapshot{snap("/run/ceph-backup-rgw/photos", t1, 0)},
		runOK:    false,
		runAt:    t2,
		runFound: true,
	}
	reg := prometheus.NewRegistry()
	c := New(reg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Lead(ctx, src, time.Hour); close(done) }()
	waitFor(t, func() bool { n, _ := testutil.GatherAndCount(reg); return n > 0 })

	want := `
# HELP homelab_ceph_backup_item Something in Ceph that should be backed up (kind=pool: found but not backed up).
# TYPE homelab_ceph_backup_item gauge
homelab_ceph_backup_item{kind="pool",name="mystery"} 1
homelab_ceph_backup_item{kind="rbd",name="media/jellyfin-config"} 1
homelab_ceph_backup_item{kind="rgw",name="never"} 1
homelab_ceph_backup_item{kind="rgw",name="photos"} 1
# HELP homelab_ceph_backup_last_run_success 1 if the newest finished CephBackup run succeeded.
# TYPE homelab_ceph_backup_last_run_success gauge
homelab_ceph_backup_last_run_success 0
# HELP homelab_ceph_backup_last_run_timestamp_seconds When the newest finished CephBackup run closed.
# TYPE homelab_ceph_backup_last_run_timestamp_seconds gauge
homelab_ceph_backup_last_run_timestamp_seconds 1.8000864e+09
# HELP homelab_ceph_backup_last_success_timestamp_seconds End time of the newest snapshot without errors of the item, from the Kopia repository.
# TYPE homelab_ceph_backup_last_success_timestamp_seconds gauge
homelab_ceph_backup_last_success_timestamp_seconds{kind="rbd",name="media/jellyfin-config"} 1.8000864e+09
homelab_ceph_backup_last_success_timestamp_seconds{kind="rgw",name="photos"} 1.8e+09
# HELP homelab_ceph_backup_storj_last_sync_timestamp_seconds End time of the newest snapshot in the Storj copy.
# TYPE homelab_ceph_backup_storj_last_sync_timestamp_seconds gauge
homelab_ceph_backup_storj_last_sync_timestamp_seconds 1.8e+09
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want)); err != nil {
		t.Error(err)
	}

	// A failing source keeps the previous values.
	src.invErr = errors.New("ceph down")
	c.Update(ctx, src)
	if n := testutil.CollectAndCount(c.item); n != 4 {
		t.Errorf("%d items after a failed inventory", n)
	}

	// Losing the Lease withdraws everything.
	cancel()
	<-done
	if n, _ := testutil.GatherAndCount(reg); n != 0 {
		t.Errorf("%d series after losing the lease", n)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

// Before the first run and the first Storj copy, their series are absent,
// not 0: a 0 would fire the failed-run alert and read as a copy from 1970.
func TestUpdateNothingYet(t *testing.T) {
	c := New(prometheus.NewRegistry())
	c.Update(context.Background(), &fakeSource{
		inv: backup.Inventory{Items: []backup.Item{{Kind: backup.KindRGW, Name: "photos"}}},
	})
	for name, m := range map[string]prometheus.Collector{
		"last_run_success": c.runSuccess, "last_run_timestamp_seconds": c.runTime,
		"storj_last_sync_timestamp_seconds": c.storjSync,
	} {
		if n := testutil.CollectAndCount(m); n != 0 {
			t.Errorf("%s: %d series, want none", name, n)
		}
	}
}
