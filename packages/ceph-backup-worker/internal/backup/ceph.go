package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"homelab/ceph-backup-worker/internal/cmdrun"
)

// Item kinds, the metrics' kind label.
const (
	KindRBD         = "rbd"
	KindRGW         = "rgw"
	KindRGWMetadata = "rgw-metadata"
	KindCephFS      = "cephfs"
	// KindPool is something found but not backed up: a pool with an
	// application the worker cannot read, an RBD namespace, or a pool the
	// cephx key cannot list. It alerts as not covered.
	KindPool = "pool"
)

// Item is one thing to back up. Kind and Name are its metric labels; the
// rest says where it is.
type Item struct {
	Kind string
	Name string
	// rbd: the image, and for ceph-csi volumes their PVC.
	Pool, Image       string `json:",omitempty"`
	PVCNamespace, PVC string `json:",omitempty"`
	Bucket, FS        string `json:",omitempty"`
}

// Inventory is what Discover found, in the order it is backed up: RBD
// images, RGW metadata, buckets, filesystems, then the uncovered pools.
// Errors are what discovery could not read; each fails the run.
type Inventory struct {
	Items  []Item
	Errors []string `json:",omitempty"`
}

func (a *Activities) ceph(ctx context.Context, name string, args ...string) ([]byte, error) {
	return a.Run.Run(ctx, cmdrun.Cmd{Name: name, Args: append(a.Cfg.cephArgs(), args...)})
}

func (a *Activities) cephJSON(ctx context.Context, v any, name string, args ...string) error {
	out, err := a.ceph(ctx, name, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

type cephPool struct {
	Name string                     `json:"pool_name"`
	Apps map[string]json.RawMessage `json:"application_metadata"`
}

type cephFS struct {
	Name         string   `json:"name"`
	MetadataPool string   `json:"metadata_pool"`
	DataPools    []string `json:"data_pools"`
}

func (a *Activities) pools(ctx context.Context) ([]cephPool, error) {
	var p []cephPool
	return p, a.cephJSON(ctx, &p, "ceph", "osd", "pool", "ls", "detail", "--format", "json")
}

func rbdPools(pools []cephPool) []string {
	var out []string
	for _, p := range pools {
		if _, ok := p.Apps["rbd"]; ok {
			out = append(out, p.Name)
		}
	}
	return out
}

// rbdList is every image in pool, this worker's clones included.
func (a *Activities) rbdList(ctx context.Context, pool string) ([]string, error) {
	var imgs []string
	if err := a.cephJSON(ctx, &imgs, "rbd", "ls", "--format", "json", pool); err != nil {
		return nil, poolErr(pool, err)
	}
	return imgs, nil
}

// poolErr explains a permission error: client.ceph-backup's OSD caps name
// its RBD pools one by one.
func poolErr(pool string, err error) error {
	s := err.Error()
	if strings.Contains(s, "Operation not permitted") || strings.Contains(s, "Permission denied") {
		return fmt.Errorf("client.ceph-backup has no caps on pool %s; rerun docs/ceph-bootstrap/07-ceph-backup-worker.sh: %w", pool, err)
	}
	return err
}

func ownName(name string) bool { return strings.HasPrefix(name, Prefix+"-") }

// imageMeta is a ceph-csi PVC field of an image, or "" if it has none.
func (a *Activities) imageMeta(ctx context.Context, pool, img, field string) string {
	out, err := a.ceph(ctx, "rbd", "image-meta", "get", pool+"/"+img, "csi.storage.k8s.io/pvc/"+field)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// rbdItem names an image: ceph-csi's by their PVC, any other by pool and
// name.
func (a *Activities) rbdItem(ctx context.Context, pool, img string) Item {
	it := Item{Kind: KindRBD, Pool: pool, Image: img, Name: pool + "/" + img}
	ns, pvc := a.imageMeta(ctx, pool, img, "namespace"), a.imageMeta(ctx, pool, img, "name")
	if ns != "" && pvc != "" {
		it.PVCNamespace, it.PVC, it.Name = ns, pvc, ns+"/"+pvc
	}
	return it
}

// discover lists everything that should be backed up. It changes nothing.
func (a *Activities) discover(ctx context.Context) (Inventory, error) {
	var inv Inventory
	pools, err := a.pools(ctx)
	if err != nil {
		return inv, err
	}
	var fss []cephFS
	if err := a.cephJSON(ctx, &fss, "ceph", "fs", "ls", "--format", "json"); err != nil {
		return inv, err
	}
	var uncovered []Item
	for _, pool := range rbdPools(pools) {
		imgs, err := a.rbdList(ctx, pool)
		if err != nil {
			inv.Errors = append(inv.Errors, err.Error())
			uncovered = append(uncovered, Item{Kind: KindPool, Name: pool})
			continue
		}
		for _, img := range imgs {
			if !ownName(img) {
				inv.Items = append(inv.Items, a.rbdItem(ctx, pool, img))
			}
		}
		// rbd ls sees only the default namespace.
		var nss []struct {
			Name string `json:"name"`
		}
		if err := a.cephJSON(ctx, &nss, "rbd", "namespace", "ls", "--format", "json", pool); err != nil {
			inv.Errors = append(inv.Errors, poolErr(pool, err).Error())
		}
		for _, ns := range nss {
			uncovered = append(uncovered, Item{Kind: KindPool, Name: pool + "/" + ns.Name})
		}
	}
	if hasApp(pools, "rgw") {
		inv.Items = append(inv.Items, Item{Kind: KindRGWMetadata, Name: "default"})
		buckets, err := a.Admin.MetadataKeys(ctx, "bucket")
		if err != nil {
			inv.Errors = append(inv.Errors, fmt.Sprintf("listing buckets: %v", err))
		}
		sort.Strings(buckets)
		for _, b := range buckets {
			inv.Items = append(inv.Items, Item{Kind: KindRGW, Name: b, Bucket: b})
		}
	}
	for _, fs := range fss {
		inv.Items = append(inv.Items, Item{Kind: KindCephFS, Name: fs.Name, FS: fs.Name})
	}
	for _, p := range uncoveredPools(pools, fss) {
		uncovered = append(uncovered, Item{Kind: KindPool, Name: p})
	}
	inv.Items = append(inv.Items, uncovered...)
	return inv, nil
}

func hasApp(pools []cephPool, app string) bool {
	for _, p := range pools {
		if _, ok := p.Apps[app]; ok {
			return true
		}
	}
	return false
}

// uncoveredPools are the pools no backup reads: rgw pools are covered by the
// bucket and metadata backups, a filesystem's pools by its backup, and .mgr
// holds only mgr module state.
func uncoveredPools(pools []cephPool, fss []cephFS) []string {
	fsPools := map[string]bool{}
	for _, fs := range fss {
		fsPools[fs.MetadataPool] = true
		for _, d := range fs.DataPools {
			fsPools[d] = true
		}
	}
	var out []string
	for _, p := range pools {
		covered := false
		for app := range p.Apps {
			if app == "rbd" || app == "rgw" || app == "mgr" || (app == "cephfs" && fsPools[p.Name]) {
				covered = true
			}
		}
		if !covered {
			out = append(out, p.Name)
		}
	}
	return out
}
