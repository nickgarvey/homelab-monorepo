package backup

import "testing"

func TestOwnMount(t *testing.T) {
	c := &Config{MountRoot: "/run"}
	for path, want := range map[string]bool{
		"/run/ceph-backup":                         true,
		"/run/ceph-backup/couchdb/couchdb-data":    true,
		"/run/ceph-backup-rgw/celler":              true,
		"/run/ceph-backup-rgw-metadata":            true,
		"/run/ceph-backup-cephfs-live/local-drive": true,
		"/run/ceph-backup-worker":                  false,
		"/run/ceph-backup-worker/kopia":            false,
		"/run/ceph-backupx":                        false,
		"/run/spiffe":                              false,
	} {
		if got := c.ownMount(path); got != want {
			t.Errorf("ownMount(%q) = %v, want %v", path, got, want)
		}
	}
}
