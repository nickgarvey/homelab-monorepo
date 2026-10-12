// Package backup is the CephBackup workflow and its activities: a port of
// the bash job in modules/services/ceph-backup.nix, item for item. The
// activities shell out to rbd, ceph, rados, mount, rclone and kopia, and keep
// that job's mount paths and Kopia sources, so snapshot history continues.
package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Prefix names the RBD snapshots and clones and the CephFS snapshots this
// worker makes. A leftover RBD snapshot or clone stops ceph-csi from deleting
// its image, so everything with it is cleaned up on sight.
const Prefix = "kopia-backup"

// Config is where everything is: the Ceph, RGW, Garage and Storj
// credentials as Kubernetes mounts them, and the worker's own directories.
// Credential files are read when used, so a rotated secret needs no restart.
type Config struct {
	// Ceph CLI and kernel client: ceph.conf (mon_host, ms_*_mode), the
	// cephx user (client.<CephID>) and its keyring.
	CephConf    string
	CephID      string
	CephKeyring string

	// RGW S3 and admin-ops endpoint; the admin-ops user's keys are the
	// files access_key and secret_key in RGWAdminDir. Buckets are read
	// with STS credentials from the environment (Activities.Creds).
	RGWEndpoint string
	RGWAdminDir string
	// Pool holding the OIDC provider objects (namespace oidc).
	RGWMetaPool string

	// Kopia repository on Garage: garage_key_id, garage_secret_key and
	// kopia_password in KopiaDir.
	KopiaDir       string
	GarageEndpoint string
	GarageBucket   string
	GarageRegion   string
	// Off-site copy on Storj's hosted S3 gateway: access_key, secret_key in
	// StorjDir.
	StorjDir      string
	StorjEndpoint string
	StorjBucket   string

	// StateDir keeps Kopia's caches across pod restarts (a hostPath).
	StateDir string
	// RunDir is pod-local: Kopia's repository configs (which hold the
	// storage keys, so they stay off the host's disk), its logs and
	// short-lived secret files.
	RunDir string
	// MountRoot is where the Kopia sources are mounted: /run, giving
	// /run/ceph-backup/<ns>/<pvc> and so on. Only tests change it.
	MountRoot string

	// KopiaParallel caps the files Kopia uploads at once (0: Kopia's
	// default), which bounds its memory on a first full copy.
	KopiaParallel int
}

// cephArgs are the global options of rbd, ceph and rados.
func (c *Config) cephArgs() []string {
	var a []string
	if c.CephConf != "" {
		a = append(a, "--conf", c.CephConf)
	}
	if c.CephID != "" {
		a = append(a, "--id", c.CephID)
	}
	if c.CephKeyring != "" {
		a = append(a, "--keyring", c.CephKeyring)
	}
	return a
}

// Kopia sources. Kopia identifies a source by its path, so these are the
// bash job's and must not change. ceph-csi volumes are at
// /run/ceph-backup/<namespace>/<pvc>; everything else has its own root, so a
// namespace name cannot collide with it.
func (c *Config) mnt(parts ...string) string {
	return filepath.Join(append([]string{c.MountRoot}, parts...)...)
}
func (c *Config) pvcPath(ns, pvc string) string   { return c.mnt("ceph-backup", ns, pvc) }
func (c *Config) rbdPath(pool, img string) string { return c.mnt("ceph-backup-rbd", pool, img) }
func (c *Config) bucketPath(b string) string      { return c.mnt("ceph-backup-rgw", b) }
func (c *Config) rgwMetadataPath() string         { return c.mnt("ceph-backup-rgw-metadata") }
func (c *Config) cephfsPath(fs string) string     { return c.mnt("ceph-backup-cephfs", fs) }
func (c *Config) cephfsLivePath(fs string) string { return c.mnt("ceph-backup-cephfs-live", fs) }

// ownMount: path is one of the roots above or under one. Exact roots, not a
// string prefix: "/run/ceph-backup" would also match the worker's own
// /run/ceph-backup-worker.
func (c *Config) ownMount(path string) bool {
	for _, r := range []string{"ceph-backup", "ceph-backup-rbd", "ceph-backup-rgw", "ceph-backup-rgw-metadata", "ceph-backup-cephfs", "ceph-backup-cephfs-live"} {
		root := c.mnt(r)
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

// readSecret reads one credential file, trimmed.
func readSecret(dir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", fmt.Errorf("%s is empty", filepath.Join(dir, name))
	}
	return s, nil
}

// keyringKey is the base64 key in a cephx keyring ("key = ...").
func keyringKey(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "key" {
			return strings.TrimSpace(v), nil
		}
	}
	return "", fmt.Errorf("%s: no key", path)
}
