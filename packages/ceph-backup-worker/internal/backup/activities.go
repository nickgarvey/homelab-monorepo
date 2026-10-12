package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"go.temporal.io/sdk/temporal"

	"homelab/ceph-backup-worker/internal/cmdrun"
)

// Activities are the CephBackup workflow's activities.
type Activities struct {
	Cfg   Config
	Run   cmdrun.Runner
	Admin RGWAdmin
	// Creds are the STS credentials buckets are read with, fetched afresh
	// for each bucket.
	Creds func(ctx context.Context) (aws.Credentials, error)
	// Heartbeat is activity.RecordHeartbeat in the worker; a no-op in tests.
	Heartbeat func(ctx context.Context)
	// Now is the clock for snapshot names.
	Now func() time.Time

	connMu sync.Mutex
}

// ErrTypeVerify is the error type of a snapshot that failed its checks:
// retrying does not change the snapshot, so it is not retried.
const ErrTypeVerify = "VerifyFailed"

// HeartbeatInterval is how often a long activity heartbeats.
var HeartbeatInterval = 30 * time.Second

// rcloneExitWait is how long an rclone mount gets to exit after its unmount.
var rcloneExitWait = 10 * time.Second

func (a *Activities) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Activities) heartbeat(ctx context.Context) {
	if a.Heartbeat != nil {
		a.Heartbeat(ctx)
	}
}

// heartbeating runs f, heartbeating every HeartbeatInterval until it
// returns.
func (a *Activities) heartbeating(ctx context.Context, f func(context.Context) error) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(HeartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				a.heartbeat(ctx)
			}
		}
	}()
	return f(ctx)
}

// undo is the steps that take back what an activity set up, run last first.
type undo []func() error

func (u *undo) push(f func() error) { *u = append(*u, f) }

// run runs every step, even after one fails, and joins err with their
// errors.
func (u undo) run(err error) error {
	errs := []error{err}
	for i := len(u) - 1; i >= 0; i-- {
		errs = append(errs, u[i]())
	}
	return errors.Join(errs...)
}

// BackupResult is the snapshot a backup made, for Verify.
type BackupResult struct {
	SnapshotID string
	// Source is the snapshot's source path.
	Source string
}

func result(s Snapshot) BackupResult { return BackupResult{SnapshotID: s.ID, Source: s.Source.Path} }

// Discover cleans up what an earlier run left behind, connects to the
// repository, sets the global policy, and lists everything to back up. A
// cleanup that could not remove something is reported in Errors, so the
// run fails and alerts, but does not stop the backups.
func (a *Activities) Discover(ctx context.Context) (Inventory, error) {
	var cleanupErrs []string
	if err := a.CleanupLocal(ctx); err != nil {
		cleanupErrs = append(cleanupErrs, "cleanup: "+err.Error())
	}
	if err := a.cleanupCluster(ctx); err != nil {
		cleanupErrs = append(cleanupErrs, "cleanup: "+err.Error())
	}
	a.heartbeat(ctx)
	if err := a.connect(ctx, garage); err != nil {
		return Inventory{}, err
	}
	if _, err := a.kopia(ctx, garage, "policy", "set", "--global", "--compression=zstd-fastest",
		"--keep-latest=14", "--keep-daily=14", "--keep-hourly=0",
		"--keep-weekly=0", "--keep-monthly=0", "--keep-annual=0"); err != nil {
		return Inventory{}, err
	}
	inv, err := a.discover(ctx)
	if err != nil {
		return inv, err
	}
	inv.Errors = append(inv.Errors, cleanupErrs...)
	return inv, nil
}

func (a *Activities) stamp() string { return a.now().UTC().Format("20060102T150405Z") }

// BackupRBD snapshots the image (crash-consistent), clones the snapshot and
// mounts the clone, so the filesystem replays its journal, and snapshots the
// mount with Kopia. Clone and snapshot names carry the time, so a retry is
// not blocked by an earlier attempt's leftovers (which may still be mapped
// on a node whose worker died); Discover removes those.
func (a *Activities) BackupRBD(ctx context.Context, it Item) (res BackupResult, err error) {
	if err := a.connect(ctx, garage); err != nil {
		return res, err
	}
	var mnt string
	tags := []string{"kind:rbd"}
	if it.PVC != "" {
		mnt = a.Cfg.pvcPath(it.PVCNamespace, it.PVC)
		tags = append(tags, "namespace:"+it.PVCNamespace, "pvc:"+it.PVC)
	} else {
		mnt = a.Cfg.rbdPath(it.Pool, it.Image)
	}
	tags = append(tags, "pool:"+it.Pool, "image:"+it.Image)

	stamp := a.stamp()
	image := it.Pool + "/" + it.Image
	snap := image + "@" + Prefix + "-" + stamp
	clone := it.Pool + "/" + Prefix + "-" + it.Image + "-" + stamp
	var u undo
	defer func() { err = u.run(err) }()
	rbd := func(args ...string) (string, error) {
		out, err := a.ceph(ctx, "rbd", args...)
		if err != nil {
			err = poolErr(it.Pool, err)
		}
		return strings.TrimSpace(string(out)), err
	}

	if _, err := rbd("snap", "create", snap); err != nil {
		return res, err
	}
	u.push(func() error { _, err := rbd("snap", "rm", "--no-progress", snap); return err })
	if _, err := rbd("snap", "protect", snap); err != nil {
		return res, err
	}
	u.push(func() error { _, err := rbd("snap", "unprotect", snap); return err })
	if _, err := rbd("clone", snap, clone); err != nil {
		return res, err
	}
	u.push(func() error { _, err := rbd("rm", "--no-progress", clone); return err })
	dev, err := rbd("device", "map", clone)
	if err != nil {
		return res, err
	}
	u.push(func() error { _, err := rbd("device", "unmap", dev); return err })

	if err := os.MkdirAll(mnt, 0o700); err != nil {
		return res, err
	}
	if _, err := a.Run.Run(ctx, cmdrun.Cmd{Name: "blkid", Args: []string{"-p", dev}}); err != nil {
		if it.PVC == "" {
			return res, fmt.Errorf("%s: no filesystem; only filesystems can be backed up", it.Name)
		}
		// ceph-csi formats a volume the first time it is mounted. Until
		// then the empty mount point is snapshotted, so the volume has a
		// backup (of nothing) and its history starts.
	} else {
		if _, err := a.Run.Run(ctx, cmdrun.Cmd{Name: "mount", Args: []string{"-o", "noatime", dev, mnt}}); err != nil {
			return res, err
		}
		u.push(func() error { return a.umount(ctx, mnt) })
	}
	s, err := a.snapshotCreate(ctx, mnt, tags...)
	if err != nil {
		return res, err
	}
	return result(s), nil
}

// BackupBucket mounts the bucket with rclone, through S3 as the read-only
// STS role, and snapshots the mount.
func (a *Activities) BackupBucket(ctx context.Context, it Item) (res BackupResult, err error) {
	if err := a.connect(ctx, garage); err != nil {
		return res, err
	}
	creds, err := a.Creds(ctx)
	if err != nil {
		return res, fmt.Errorf("STS credentials: %w", err)
	}
	mnt := a.Cfg.bucketPath(it.Bucket)
	if err := os.MkdirAll(mnt, 0o700); err != nil {
		return res, err
	}
	var u undo
	defer func() { err = u.run(err) }()

	// Server modtimes come with the listing; rclone's own mtime metadata
	// would cost a HEAD per object. Kopia skips files whose size and mtime
	// are unchanged, so a run reads only new and changed objects.
	p, err := a.Run.Start(ctx, cmdrun.Cmd{
		Name: "rclone",
		Args: []string{"mount", "rgw:" + it.Bucket, mnt, "--config", "", "--read-only",
			"--use-server-modtime", "--vfs-cache-mode", "off", "--dir-cache-time", "1h"},
		Env: []string{
			"RCLONE_CONFIG_RGW_TYPE=s3",
			"RCLONE_CONFIG_RGW_PROVIDER=Ceph",
			"RCLONE_CONFIG_RGW_ENDPOINT=" + a.Cfg.RGWEndpoint,
			"RCLONE_CONFIG_RGW_ACCESS_KEY_ID=" + creds.AccessKeyID,
			"RCLONE_CONFIG_RGW_SECRET_ACCESS_KEY=" + creds.SecretAccessKey,
			"RCLONE_CONFIG_RGW_SESSION_TOKEN=" + creds.SessionToken,
		},
	})
	if err != nil {
		return res, err
	}
	exited := make(chan error, 1)
	go func() { exited <- p.Wait() }()
	u.push(func() error {
		select {
		case <-exited:
		case <-time.After(rcloneExitWait):
			p.Kill()
			<-exited
		}
		return nil
	})
	if err := a.waitMounted(ctx, mnt, exited); err != nil {
		return res, err
	}
	u.push(func() error { return a.umount(ctx, mnt) })

	s, err := a.snapshotCreate(ctx, mnt, "kind:rgw", "bucket:"+it.Bucket)
	if err != nil {
		return res, err
	}
	return result(s), nil
}

// waitMounted waits for rclone's mount to appear at mnt, for a minute.
func (a *Activities) waitMounted(ctx context.Context, mnt string, exited <-chan error) error {
	deadline := time.Now().Add(time.Minute)
	for {
		if _, err := a.Run.Run(ctx, cmdrun.Cmd{Name: "mountpoint", Args: []string{"-q", mnt}}); err == nil {
			return nil
		}
		select {
		case err := <-exited:
			return fmt.Errorf("rclone mount exited before mounting: %v", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rclone did not mount %s within a minute", mnt)
		}
	}
}

// BackupRGWMetadata exports RGW's metadata through the admin-ops API (every
// section: users with their keys, accounts, roles, buckets and their
// instances, ...) and the OIDC provider objects, which are not metadata the
// API exports, with rados; then snapshots the directory. The zone and
// zonegroup are not exported: the admin API does not serve them, and RGW
// recreates the default zone itself.
func (a *Activities) BackupRGWMetadata(ctx context.Context, it Item) (res BackupResult, err error) {
	if err := a.connect(ctx, garage); err != nil {
		return res, err
	}
	dir := a.Cfg.rgwMetadataPath()
	if err := os.RemoveAll(dir); err != nil {
		return res, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, err
	}
	// It holds users' secret keys.
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()

	sections, err := a.Admin.MetadataSections(ctx)
	if err != nil {
		return res, err
	}
	for _, section := range sections {
		sdir := filepath.Join(dir, section)
		if err := os.MkdirAll(sdir, 0o700); err != nil {
			return res, err
		}
		keys, err := a.Admin.MetadataKeys(ctx, section)
		if err != nil {
			return res, fmt.Errorf("metadata section %s: %w", section, err)
		}
		for _, key := range keys {
			body, err := a.Admin.MetadataGet(ctx, section, key)
			if err != nil {
				return res, fmt.Errorf("metadata %s:%s: %w", section, key, err)
			}
			name := strings.ReplaceAll(key, "/", "%") + ".json"
			if err := os.WriteFile(filepath.Join(sdir, name), body, 0o600); err != nil {
				return res, err
			}
		}
		a.heartbeat(ctx)
	}

	// Each OIDC provider is one small object (no xattrs or omap), restored
	// with `rados put`.
	odir := filepath.Join(dir, "oidc")
	if err := os.MkdirAll(odir, 0o700); err != nil {
		return res, err
	}
	out, err := a.ceph(ctx, "rados", "-p", a.Cfg.RGWMetaPool, "-N", "oidc", "ls")
	if err != nil {
		return res, err
	}
	for _, o := range strings.Fields(string(out)) {
		if _, err := a.ceph(ctx, "rados", "-p", a.Cfg.RGWMetaPool, "-N", "oidc", "get", o, filepath.Join(odir, o)); err != nil {
			return res, err
		}
	}

	s, err := a.snapshotCreate(ctx, dir, "kind:rgw-metadata")
	if err != nil {
		return res, err
	}
	return result(s), nil
}

// BackupCephFS snapshots the filesystem's root (mkdir .snap/<name>),
// bind-mounts the snapshot at a fixed path, so Kopia sees the same source
// every day, and snapshots that.
func (a *Activities) BackupCephFS(ctx context.Context, it Item) (res BackupResult, err error) {
	if err := a.connect(ctx, garage); err != nil {
		return res, err
	}
	out, err := a.ceph(ctx, "ceph", "fsid")
	if err != nil {
		return res, err
	}
	fsid := strings.TrimSpace(string(out))
	live, mnt := a.Cfg.cephfsLivePath(it.FS), a.Cfg.cephfsPath(it.FS)
	for _, d := range []string{live, mnt} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return res, err
		}
	}
	var u undo
	defer func() { err = u.run(err) }()

	key, err := keyringKey(a.Cfg.CephKeyring)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(a.Cfg.RunDir, 0o700); err != nil {
		return res, err
	}
	secret, err := os.CreateTemp(a.Cfg.RunDir, "cephfs-secret-")
	if err != nil {
		return res, err
	}
	_, werr := secret.WriteString(key)
	cerr := secret.Close()
	defer os.Remove(secret.Name())
	if err := errors.Join(werr, cerr); err != nil {
		return res, err
	}
	opts := "secretfile=" + secret.Name() + ",ms_mode=secure,noatime"
	if a.Cfg.CephConf != "" {
		opts += ",conf=" + a.Cfg.CephConf
	}
	if _, err := a.Run.Run(ctx, cmdrun.Cmd{Name: "mount.ceph",
		Args: []string{a.Cfg.CephID + "@" + fsid + "." + it.FS + "=/", live, "-o", opts}}); err != nil {
		return res, err
	}
	u.push(func() error { return a.umount(ctx, live) })
	os.Remove(secret.Name())

	old, _ := filepath.Glob(filepath.Join(live, ".snap", Prefix+"-*"))
	for _, s := range old {
		if err := os.Remove(s); err != nil {
			return res, err
		}
	}
	snapDir := filepath.Join(live, ".snap", Prefix+"-"+a.stamp())
	if err := os.Mkdir(snapDir, 0o755); err != nil {
		return res, err
	}
	u.push(func() error { return os.Remove(snapDir) })
	if _, err := a.Run.Run(ctx, cmdrun.Cmd{Name: "mount", Args: []string{"--bind", "-o", "ro", snapDir, mnt}}); err != nil {
		return res, err
	}
	u.push(func() error { return a.umount(ctx, mnt) })

	s, err := a.snapshotCreate(ctx, mnt, "kind:cephfs", "fs:"+it.FS)
	if err != nil {
		return res, err
	}
	return result(s), nil
}

// VerifyInput names the snapshot to verify and how much of it to read back.
type VerifyInput struct {
	BackupResult
	Percent float64
}

// Verify checks a snapshot right after it was taken: Kopia's record of it
// (checkSnapshot), then a read-back of Percent of its files from the
// repository.
func (a *Activities) Verify(ctx context.Context, in VerifyInput) error {
	if err := a.connect(ctx, garage); err != nil {
		return err
	}
	snaps, err := a.snapshots(ctx, garage, in.Source)
	if err != nil {
		return err
	}
	var this, prev *Snapshot
	for i := range snaps {
		if snaps[i].ID == in.SnapshotID {
			this = &snaps[i]
		}
	}
	if this == nil {
		return fmt.Errorf("snapshot %s of %s not in the repository", in.SnapshotID, in.Source)
	}
	for i := range snaps {
		s := &snaps[i]
		if s.ID != this.ID && s.StartTime.Before(this.StartTime) && (prev == nil || s.StartTime.After(prev.StartTime)) {
			prev = s
		}
	}
	if err := checkSnapshot(this, prev); err != nil {
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeVerify, nil)
	}
	if err := a.verifySnapshots(ctx, garage, in.Percent, in.SnapshotID); err != nil {
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeVerify, nil)
	}
	return nil
}

// checkSnapshot fails a snapshot with errors, or one that is empty or under
// half the size or file count of the previous snapshot of its source when
// that one was not empty: a source that silently stopped being readable
// alerts instead of being backed up as nothing.
func checkSnapshot(s, prev *Snapshot) error {
	failed := s.Stats.ErrorCount
	if s.RootEntry != nil && s.RootEntry.Summary != nil && s.RootEntry.Summary.Failed > failed {
		failed = s.RootEntry.Summary.Failed
	}
	if failed > 0 || s.Stats.IgnoredErrorCount > 0 {
		return fmt.Errorf("snapshot %s of %s has %d errors and %d ignored errors",
			s.ID, s.Source.Path, failed, s.Stats.IgnoredErrorCount)
	}
	if prev == nil {
		return nil
	}
	size, files := s.totals()
	psize, pfiles := prev.totals()
	if psize == 0 && pfiles == 0 {
		return nil
	}
	if size*2 < psize || files*2 < pfiles {
		return fmt.Errorf("snapshot %s of %s has %d bytes in %d files, under half of the previous one's (%s: %d bytes in %d files)",
			s.ID, s.Source.Path, size, files, prev.ID, psize, pfiles)
	}
	return nil
}

// Maintenance runs Kopia's maintenance: quick daily, full when full is set,
// which also reads back every file of the newest snapshot of every source.
func (a *Activities) Maintenance(ctx context.Context, full bool) error {
	if err := a.connect(ctx, garage); err != nil {
		return err
	}
	args := []string{"maintenance", "run"}
	if full {
		args = append(args, "--full")
	}
	if _, err := a.kopiaLong(ctx, garage, args...); err != nil {
		return err
	}
	if !full {
		return nil
	}
	snaps, err := a.snapshots(ctx, garage, "")
	if err != nil {
		return err
	}
	newest := newestPerSource(snaps)
	if len(newest) == 0 {
		return nil
	}
	return a.verifySnapshots(ctx, garage, 100, ids(newest)...)
}

// SyncStorj copies the repository blob for blob to the Storj bucket
// (storjArgs), deleting what the repository no longer has.
func (a *Activities) SyncStorj(ctx context.Context) error {
	if err := a.connect(ctx, garage); err != nil {
		return err
	}
	creds, err := a.storageEnv(storj)
	if err != nil {
		return err
	}
	env, err := a.kopiaEnv(garage, creds...)
	if err != nil {
		return err
	}
	return a.heartbeating(ctx, func(ctx context.Context) error {
		args := append([]string{"repository", "sync-to", "rclone", "--no-progress"}, a.storjArgs()...)
		_, err := a.Run.Run(ctx, cmdrun.Cmd{Name: "kopia", Env: env, Args: append(args, "--delete", "--parallel=8")})
		return err
	})
}

// VerifyStorj opens the Storj copy as a repository of its own and checks it
// has the newest snapshot of every source the repository has, then reads
// back 1% of their files.
func (a *Activities) VerifyStorj(ctx context.Context) error {
	if err := a.connect(ctx, garage); err != nil {
		return err
	}
	if err := a.connect(ctx, storj); err != nil {
		return err
	}
	want, err := a.snapshots(ctx, garage, "")
	if err != nil {
		return err
	}
	got, err := a.snapshots(ctx, storj, "")
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, s := range got {
		have[s.ID] = true
	}
	var missing []string
	for _, s := range newestPerSource(want) {
		if !have[s.ID] {
			missing = append(missing, s.Source.Path+" "+s.ID)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the Storj copy lacks the newest snapshot of: %s", strings.Join(missing, ", "))
	}
	newest := newestPerSource(got)
	if len(newest) == 0 {
		return errors.New("the Storj copy has no snapshots")
	}
	return a.verifySnapshots(ctx, storj, 1, ids(newest)...)
}

// Snapshots lists every snapshot in the repository, for the metrics.
func (a *Activities) Snapshots(ctx context.Context) ([]Snapshot, error) {
	if err := a.connect(ctx, garage); err != nil {
		return nil, err
	}
	return a.snapshots(ctx, garage, "")
}

// StorjSnapshots lists every snapshot in the Storj copy, for the metrics.
func (a *Activities) StorjSnapshots(ctx context.Context) ([]Snapshot, error) {
	if err := a.connect(ctx, storj); err != nil {
		return nil, err
	}
	return a.snapshots(ctx, storj, "")
}

// Inventory lists what should be backed up, changing nothing, for the
// metrics.
func (a *Activities) Inventory(ctx context.Context) (Inventory, error) { return a.discover(ctx) }
