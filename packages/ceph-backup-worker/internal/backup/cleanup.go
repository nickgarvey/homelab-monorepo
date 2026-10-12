package backup

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"homelab/ceph-backup-worker/internal/cmdrun"
)

// CleanupLocal unmounts this pod's leftover mounts under the Kopia source
// roots and unmaps this node's leftover kopia-backup-* RBD devices. The
// worker runs it at start, after a crash may have left them. Finding nothing
// is normal; failing to remove something is an error.
func (a *Activities) CleanupLocal(ctx context.Context) error {
	var errs []error
	out, err := a.Run.Run(ctx, cmdrun.Cmd{Name: "findmnt", Args: []string{"-rn", "-o", "TARGET"}})
	if err != nil {
		errs = append(errs, err)
	}
	var mounts []string
	for _, m := range strings.Split(string(out), "\n") {
		// findmnt -r escapes spaces and such as \x20; ours have none.
		if a.Cfg.ownMount(m) {
			mounts = append(mounts, m)
		}
	}
	// Deepest first: a bind mount of a CephFS snapshot sits on another root
	// than its filesystem, but a nested mount must go before its parent.
	sort.Sort(sort.Reverse(sort.StringSlice(mounts)))
	for _, m := range mounts {
		if err := a.umount(ctx, m); err != nil {
			if _, err2 := a.Run.Run(ctx, cmdrun.Cmd{Name: "umount", Args: []string{"-l", m}}); err2 != nil {
				errs = append(errs, fmt.Errorf("%w; lazy: %w", err, err2))
			}
		}
	}

	var devs []struct {
		Name   string `json:"name"`
		Device string `json:"device"`
	}
	if err := a.cephJSON(ctx, &devs, "rbd", "device", "list", "--format", "json"); err != nil {
		errs = append(errs, err)
	}
	for _, d := range devs {
		if ownName(d.Name) {
			if _, err := a.ceph(ctx, "rbd", "device", "unmap", d.Device); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// cleanupCluster removes leftover kopia-backup-* clones and snapshots from
// every RBD pool, after CleanupLocal. A clone still mapped on another node
// (whose worker died mid-backup) cannot be removed until that node's worker
// restarts and unmaps it; that is reported, and the next run tries again.
func (a *Activities) cleanupCluster(ctx context.Context) error {
	pools, err := a.pools(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, pool := range rbdPools(pools) {
		imgs, err := a.rbdList(ctx, pool)
		if err != nil {
			// Reported by discovery.
			continue
		}
		// Clones first: a protected snapshot with a clone cannot go.
		for _, img := range imgs {
			if ownName(img) {
				if _, err := a.ceph(ctx, "rbd", "rm", "--no-progress", pool+"/"+img); err != nil {
					errs = append(errs, fmt.Errorf("leftover clone %s/%s: %w", pool, img, err))
				}
			}
		}
		for _, img := range imgs {
			if ownName(img) {
				continue
			}
			var snaps []struct {
				Name string `json:"name"`
			}
			if err := a.cephJSON(ctx, &snaps, "rbd", "snap", "ls", "--format", "json", pool+"/"+img); err != nil {
				errs = append(errs, err)
				continue
			}
			for _, s := range snaps {
				if !ownName(s.Name) {
					continue
				}
				spec := pool + "/" + img + "@" + s.Name
				_, _ = a.ceph(ctx, "rbd", "snap", "unprotect", spec)
				if _, err := a.ceph(ctx, "rbd", "snap", "rm", "--no-progress", spec); err != nil {
					errs = append(errs, fmt.Errorf("leftover snapshot %s: %w", spec, err))
				}
			}
		}
	}
	return errors.Join(errs...)
}

func (a *Activities) umount(ctx context.Context, path string) error {
	_, err := a.Run.Run(ctx, cmdrun.Cmd{Name: "umount", Args: []string{path}})
	return err
}
