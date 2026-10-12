package backup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"homelab/ceph-backup-worker/internal/cmdrun"
)

// Shaped like the real outputs of the Ceph CLIs on this cluster.
const (
	poolsJSON = `[
 {"pool_id":1,"pool_name":".mgr","application_metadata":{"mgr":{}}},
 {"pool_id":2,"pool_name":"kubernetes","application_metadata":{"rbd":{}}},
 {"pool_id":3,"pool_name":".rgw.root","application_metadata":{"rgw":{}}},
 {"pool_id":4,"pool_name":"default.rgw.meta","application_metadata":{"rgw":{}}},
 {"pool_id":5,"pool_name":"cephfs.local-drive.meta","application_metadata":{"cephfs":{"metadata":"local-drive"}}},
 {"pool_id":6,"pool_name":"cephfs.local-drive.data","application_metadata":{"cephfs":{"data":"local-drive"}}},
 {"pool_id":7,"pool_name":"stray","application_metadata":{"cephfs":{}}},
 {"pool_id":8,"pool_name":"other-rbd","application_metadata":{"rbd":{}}},
 {"pool_id":9,"pool_name":"mystery","application_metadata":{}}
]`
	fsJSON = `[{"name":"local-drive","metadata_pool":"cephfs.local-drive.meta","metadata_pool_id":5,
  "data_pool_ids":[6],"data_pools":["cephfs.local-drive.data"]}]`
)

type fakeAdmin struct {
	keys map[string][]string
}

func (f *fakeAdmin) MetadataSections(context.Context) ([]string, error) {
	var s []string
	for k := range f.keys {
		s = append(s, k)
	}
	return s, nil
}

func (f *fakeAdmin) MetadataKeys(_ context.Context, section string) ([]string, error) {
	return f.keys[section], nil
}

func (f *fakeAdmin) MetadataGet(_ context.Context, section, key string) ([]byte, error) {
	return []byte(`{"key":"` + section + ":" + key + `"}`), nil
}

func newActs(t *testing.T, run *cmdrun.Fake) *Activities {
	t.Helper()
	dir := t.TempDir()
	write := func(sub, name, v string) {
		p := filepath.Join(dir, sub)
		os.MkdirAll(p, 0o700)
		if err := os.WriteFile(filepath.Join(p, name), []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("kopia", "garage_key_id", "GK1")
	write("kopia", "garage_secret_key", "gs")
	write("kopia", "kopia_password", "pw")
	write("storj", "access_key", "SK1")
	write("storj", "secret_key", "ss")
	write("ceph", "keyring", "[client.ceph-backup]\n\tkey = QUJDRA==")
	keyring := filepath.Join(dir, "ceph", "keyring")
	// The cephx options every Ceph CLI call has.
	run.Normalize = func(l string) string { return strings.Replace(l, " --id ceph-backup --keyring "+keyring, "", 1) }
	return &Activities{
		Cfg: Config{
			CephID: "ceph-backup", CephKeyring: filepath.Join(dir, "ceph", "keyring"),
			RGWEndpoint: "https://rgw.example", RGWMetaPool: "default.rgw.meta",
			KopiaDir: filepath.Join(dir, "kopia"), GarageEndpoint: "garage.example", GarageBucket: "ceph-backup", GarageRegion: "garage",
			StorjDir: filepath.Join(dir, "storj"), StorjEndpoint: "gateway.example", StorjBucket: "ceph-backup",
			StateDir: filepath.Join(dir, "state"), RunDir: filepath.Join(dir, "run"), MountRoot: filepath.Join(dir, "mnt"),
		},
		Run:   run,
		Admin: &fakeAdmin{keys: map[string][]string{"bucket": {"photos", "chats"}, "user": {"a"}}},
		Now:   func() time.Time { return time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC) },
	}
}

func discoveryFake() *cmdrun.Fake {
	f := &cmdrun.Fake{}
	f.On("ceph osd pool ls detail --format json", poolsJSON)
	f.On("ceph fs ls --format json", fsJSON)
	f.On("rbd ls --format json kubernetes", `["csi-vol-1","csi-vol-2","kopia-backup-csi-vol-1-20261011T100000Z","plain"]`)
	f.Fail("rbd ls --format json other-rbd", "rbd: error opening pool 'other-rbd': (1) Operation not permitted")
	f.On("rbd namespace ls --format json kubernetes", `[{"name":"tenant"}]`)
	f.On("rbd image-meta get kubernetes/csi-vol-1 csi.storage.k8s.io/pvc/namespace", "media\n")
	f.On("rbd image-meta get kubernetes/csi-vol-1 csi.storage.k8s.io/pvc/name", "jellyfin-config\n")
	f.On("rbd image-meta get kubernetes/csi-vol-2 csi.storage.k8s.io/pvc/namespace", "db\n")
	f.On("rbd image-meta get kubernetes/csi-vol-2 csi.storage.k8s.io/pvc/name", "data-postgres-0\n")
	f.Fail("rbd image-meta get kubernetes/plain", "failed to get metadata")
	return f
}

func TestDiscover(t *testing.T) {
	a := newActs(t, discoveryFake())
	inv, err := a.discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range inv.Items {
		got = append(got, it.Kind+" "+it.Name)
	}
	want := []string{
		"rbd media/jellyfin-config",
		"rbd db/data-postgres-0",
		"rbd kubernetes/plain",
		"rgw-metadata default",
		"rgw chats",
		"rgw photos",
		"cephfs local-drive",
		"pool kubernetes/tenant",
		"pool other-rbd",
		"pool stray",
		"pool mystery",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("items\n got %q\nwant %q", got, want)
	}
	if inv.Items[0].Pool != "kubernetes" || inv.Items[0].Image != "csi-vol-1" || inv.Items[0].PVC != "jellyfin-config" {
		t.Errorf("first item %+v", inv.Items[0])
	}
	if len(inv.Errors) != 1 || !strings.Contains(inv.Errors[0], "client.ceph-backup has no caps on pool other-rbd") {
		t.Errorf("errors %q", inv.Errors)
	}
}

func TestCephArgs(t *testing.T) {
	f := discoveryFake()
	a := newActs(t, f)
	a.Cfg.CephConf, a.Cfg.CephKeyring = "/etc/c.conf", "/etc/k"
	a.ceph(context.Background(), "rbd", "ls")
	if f.Calls[0] != "rbd --conf /etc/c.conf --id ceph-backup --keyring /etc/k ls" {
		t.Errorf("call %q", f.Calls[0])
	}
}

func TestCleanup(t *testing.T) {
	f := discoveryFake()
	a := newActs(t, f)
	root := a.Cfg.MountRoot
	f.On("findmnt -rn -o TARGET", "/\n/proc\n"+root+"/ceph-backup-cephfs-live/local-drive\n"+root+"/ceph-backup/media/jellyfin-config\n/run/other\n")
	f.On("umount", "")
	f.On("rbd device list --format json",
		`[{"id":"0","pool":"kubernetes","namespace":"","name":"kopia-backup-csi-vol-1-20261011T100000Z","snap":"-","device":"/dev/rbd0"},
		  {"id":"1","pool":"kubernetes","namespace":"","name":"csi-vol-9","snap":"-","device":"/dev/rbd1"}]`)
	f.On("rbd device unmap", "")
	f.On("rbd rm --no-progress kubernetes/kopia-backup-csi-vol-1-20261011T100000Z", "")
	f.On("rbd snap ls --format json kubernetes/csi-vol-1", `[{"id":4,"name":"kopia-backup-20261011T100000Z","size":1},{"id":5,"name":"csi-snap-x","size":1}]`)
	f.On("rbd snap ls --format json kubernetes/csi-vol-2", `[]`)
	f.On("rbd snap ls --format json kubernetes/plain", `[]`)
	f.Fail("rbd snap unprotect", "not protected")
	f.On("rbd snap rm --no-progress kubernetes/csi-vol-1@kopia-backup-20261011T100000Z", "")

	if err := a.CleanupLocal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.cleanupCluster(context.Background()); err != nil {
		t.Fatal(err)
	}
	var did []string
	for _, c := range f.Calls {
		if strings.HasPrefix(c, "umount") || strings.Contains(c, "unmap") || strings.HasPrefix(c, "rbd rm") || strings.HasPrefix(c, "rbd snap rm") {
			did = append(did, strings.ReplaceAll(c, root, "R"))
		}
	}
	want := []string{
		"umount R/ceph-backup/media/jellyfin-config",
		"umount R/ceph-backup-cephfs-live/local-drive",
		"rbd device unmap /dev/rbd0",
		"rbd rm --no-progress kubernetes/kopia-backup-csi-vol-1-20261011T100000Z",
		"rbd snap rm --no-progress kubernetes/csi-vol-1@kopia-backup-20261011T100000Z",
	}
	if !reflect.DeepEqual(did, want) {
		t.Errorf("cleanup\n got %q\nwant %q", did, want)
	}
}

func snapshotJSON(id, path string) string {
	return `{"id":"` + id + `","source":{"host":"ceph-backup","userName":"root","path":"` + path + `"},
	  "startTime":"2026-10-12T10:00:00Z","endTime":"2026-10-12T10:01:00Z",
	  "stats":{"totalSize":100,"fileCount":3,"errorCount":0,"ignoredErrorCount":0},
	  "rootEntry":{"name":"x","type":"d","obj":"k1","summ":{"size":100,"files":3,"numFailed":0}}}`
}

func kopiaFake(f *cmdrun.Fake) {
	f.On("kopia repository status", "")
	f.OnFunc("kopia snapshot create", func(c cmdrun.Cmd) (string, error) {
		for _, e := range c.Env {
			if e == "KOPIA_PASSWORD=pw" {
				return snapshotJSON("snap1", c.Args[2]), nil
			}
		}
		return "", errors.New("no password")
	})
}

func TestBackupRBD(t *testing.T) {
	f := &cmdrun.Fake{}
	kopiaFake(f)
	f.On("rbd ", "")
	f.On("rbd device map", "/dev/rbd3\n")
	f.On("blkid -p /dev/rbd3", "")
	f.On("mount -o noatime /dev/rbd3", "")
	f.On("umount", "")
	a := newActs(t, f)
	it := Item{Kind: KindRBD, Name: "media/jellyfin-config", Pool: "kubernetes", Image: "csi-vol-1", PVCNamespace: "media", PVC: "jellyfin-config"}
	res, err := a.BackupRBD(context.Background(), it)
	if err != nil {
		t.Fatal(err)
	}
	mnt := a.Cfg.MountRoot + "/ceph-backup/media/jellyfin-config"
	if res.SnapshotID != "snap1" || res.Source != mnt {
		t.Errorf("result %+v", res)
	}
	var got []string
	for _, c := range f.Calls {
		if !strings.HasPrefix(c, "kopia repository") {
			got = append(got, strings.ReplaceAll(c, a.Cfg.MountRoot, "R"))
		}
	}
	stripped := strings.Join(got, "\n")
	wantSeq := strings.Join([]string{
		"rbd snap create kubernetes/csi-vol-1@kopia-backup-20261012T100000Z",
		"rbd snap protect kubernetes/csi-vol-1@kopia-backup-20261012T100000Z",
		"rbd clone kubernetes/csi-vol-1@kopia-backup-20261012T100000Z kubernetes/kopia-backup-csi-vol-1-20261012T100000Z",
		"rbd device map kubernetes/kopia-backup-csi-vol-1-20261012T100000Z",
		"blkid -p /dev/rbd3",
		"mount -o noatime /dev/rbd3 R/ceph-backup/media/jellyfin-config",
		"kopia snapshot create R/ceph-backup/media/jellyfin-config --json --no-progress --tags kind:rbd --tags namespace:media --tags pvc:jellyfin-config --tags pool:kubernetes --tags image:csi-vol-1",
		"umount R/ceph-backup/media/jellyfin-config",
		"rbd device unmap /dev/rbd3",
		"rbd rm --no-progress kubernetes/kopia-backup-csi-vol-1-20261012T100000Z",
		"rbd snap unprotect kubernetes/csi-vol-1@kopia-backup-20261012T100000Z",
		"rbd snap rm --no-progress kubernetes/csi-vol-1@kopia-backup-20261012T100000Z",
	}, "\n")
	if stripped != wantSeq {
		t.Errorf("calls\n%s\nwant\n%s", stripped, wantSeq)
	}
}

// A failure part-way still takes back everything set up so far.
func TestBackupRBDFailureUndoes(t *testing.T) {
	f := &cmdrun.Fake{}
	kopiaFake(f)
	f.On("rbd ", "")
	f.On("rbd device map", "/dev/rbd3\n")
	f.On("blkid", "")
	f.Fail("mount", "mount: wrong fs type")
	a := newActs(t, f)
	_, err := a.BackupRBD(context.Background(), Item{Kind: KindRBD, Pool: "kubernetes", Image: "plain", Name: "kubernetes/plain"})
	if err == nil || !strings.Contains(err.Error(), "wrong fs type") {
		t.Fatalf("error %v", err)
	}
	for _, p := range []string{"rbd device unmap /dev/rbd3",
		"rbd snap rm"} {
		if !f.Called(p) {
			t.Errorf("not called: %s", p)
		}
	}
	if f.Called("kopia snapshot create") {
		t.Error("snapshotted after a failed mount")
	}
}

// ceph-csi volumes never mounted have no filesystem: their empty mount point
// is snapshotted. Other images without one fail.
func TestBackupRBDNoFilesystem(t *testing.T) {
	f := &cmdrun.Fake{}
	kopiaFake(f)
	f.On("rbd ", "")
	f.On("rbd device map", "/dev/rbd3\n")
	f.Fail("blkid", "exit status 2")
	a := newActs(t, f)
	if _, err := a.BackupRBD(context.Background(), Item{Kind: KindRBD, Pool: "kubernetes", Image: "v", PVCNamespace: "n", PVC: "p"}); err != nil {
		t.Fatal(err)
	}
	if f.Called("mount") || !f.Called("kopia snapshot create") {
		t.Errorf("calls %q", f.Calls)
	}
	if _, err := a.BackupRBD(context.Background(), Item{Kind: KindRBD, Pool: "kubernetes", Image: "raw", Name: "kubernetes/raw"}); err == nil ||
		!strings.Contains(err.Error(), "no filesystem") {
		t.Errorf("raw image: %v", err)
	}
}

func TestBackupBucket(t *testing.T) {
	f := &cmdrun.Fake{}
	kopiaFake(f)
	var rcloneEnv []string
	f.OnFunc("rclone mount rgw:photos", func(c cmdrun.Cmd) (string, error) { rcloneEnv = c.Env; return "", nil })
	f.On("mountpoint -q", "")
	f.On("umount", "")
	a := newActs(t, f)
	a.Creds = func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "TOK"}, nil
	}
	res, err := a.BackupBucket(context.Background(), Item{Kind: KindRGW, Name: "photos", Bucket: "photos"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != a.Cfg.MountRoot+"/ceph-backup-rgw/photos" {
		t.Errorf("source %s", res.Source)
	}
	for _, e := range []string{"RCLONE_CONFIG_RGW_SESSION_TOKEN=TOK", "RCLONE_CONFIG_RGW_ACCESS_KEY_ID=AK", "RCLONE_CONFIG_RGW_ENDPOINT=https://rgw.example"} {
		if !contains(rcloneEnv, e) {
			t.Errorf("rclone env lacks %s: %q", e, rcloneEnv)
		}
	}
	if !f.Called("kopia snapshot create " + a.Cfg.MountRoot + "/ceph-backup-rgw/photos --json --no-progress --tags kind:rgw --tags bucket:photos") {
		t.Errorf("calls %q", f.Calls)
	}
	if !f.Called("umount " + a.Cfg.MountRoot + "/ceph-backup-rgw/photos") {
		t.Error("not unmounted")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestBackupRGWMetadata(t *testing.T) {
	f := &cmdrun.Fake{}
	f.On("kopia repository status", "")
	a := newActs(t, f)
	a.Admin = &fakeAdmin{keys: map[string][]string{"user": {"RGW1$app"}, "bucket.instance": {"photos:abc.1"}, "roles": {"x/y"}}}
	f.On("rados -p default.rgw.meta -N oidc ls", "oidc_url.spire\n")
	f.OnFunc("rados -p default.rgw.meta -N oidc get oidc_url.spire", func(c cmdrun.Cmd) (string, error) {
		return "", os.WriteFile(c.Args[len(c.Args)-1], []byte("oidc"), 0o600)
	})
	dir := a.Cfg.MountRoot + "/ceph-backup-rgw-metadata"
	var files []string
	f.OnFunc("kopia snapshot create "+dir, func(cmdrun.Cmd) (string, error) {
		filepath.Walk(dir, func(p string, fi os.FileInfo, _ error) error {
			if !fi.IsDir() {
				files = append(files, strings.TrimPrefix(p, dir+"/"))
			}
			return nil
		})
		return snapshotJSON("m1", dir), nil
	})
	if _, err := a.BackupRGWMetadata(context.Background(), Item{Kind: KindRGWMetadata, Name: "default"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"bucket.instance/photos:abc.1.json", "oidc/oidc_url.spire", "roles/x%y.json", "user/RGW1$app.json"}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("files %q, want %q", files, want)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("export left behind")
	}
}

func TestBackupCephFS(t *testing.T) {
	f := &cmdrun.Fake{}
	kopiaFake(f)
	a := newActs(t, f)
	a.Cfg.CephConf = "/etc/ceph-backup/ceph/ceph.conf"
	f.On("ceph --conf /etc/ceph-backup/ceph/ceph.conf fsid", "fsid-1\n")
	var opts string
	f.OnFunc("mount.ceph ceph-backup@fsid-1.local-drive=/", func(c cmdrun.Cmd) (string, error) {
		opts = c.Args[3]
		secret := strings.TrimPrefix(strings.Split(opts, ",")[0], "secretfile=")
		b, err := os.ReadFile(secret)
		if err != nil || string(b) != "QUJDRA==" {
			return "", errors.New("bad secret file")
		}
		return "", os.MkdirAll(filepath.Join(c.Args[1], ".snap"), 0o700)
	})
	f.On("mount --bind -o ro", "")
	f.On("umount", "")
	live := a.Cfg.MountRoot + "/ceph-backup-cephfs-live/local-drive"
	os.MkdirAll(live+"/.snap/kopia-backup-old", 0o700)
	if _, err := a.BackupCephFS(context.Background(), Item{Kind: KindCephFS, Name: "local-drive", FS: "local-drive"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(opts, ",ms_mode=secure,noatime,conf=/etc/ceph-backup/ceph/ceph.conf") {
		t.Errorf("mount options %q", opts)
	}
	if !f.Called("mount --bind -o ro " + live + "/.snap/kopia-backup-20261012T100000Z " + a.Cfg.MountRoot + "/ceph-backup-cephfs/local-drive") {
		t.Errorf("calls %q", f.Calls)
	}
	if left, _ := filepath.Glob(live + "/.snap/*"); len(left) != 0 {
		t.Errorf("snapshots left: %q", left)
	}
}

func snap(id string, start int, size, files int64) Snapshot {
	var s Snapshot
	s.ID = id
	s.Source.Host, s.Source.UserName, s.Source.Path = "ceph-backup", "root", "/run/ceph-backup/a/b"
	s.StartTime = time.Date(2026, 10, start, 10, 0, 0, 0, time.UTC)
	s.Stats.TotalSize, s.Stats.FileCount = size, files
	return s
}

func withSummary(s Snapshot, size, files int64) Snapshot {
	s.RootEntry = &struct {
		Summary *struct {
			Size   int64 `json:"size"`
			Files  int64 `json:"files"`
			Failed int64 `json:"numFailed"`
		} `json:"summ"`
	}{Summary: &struct {
		Size   int64 `json:"size"`
		Files  int64 `json:"files"`
		Failed int64 `json:"numFailed"`
	}{Size: size, Files: files}}
	return s
}

func TestCheckSnapshot(t *testing.T) {
	errs := snap("e", 2, 100, 10)
	errs.Stats.ErrorCount = 1
	ignored := snap("i", 2, 100, 10)
	ignored.Stats.IgnoredErrorCount = 2
	empty := snap("z", 1, 0, 0)
	prev := snap("p", 1, 100, 10)
	// Stats counted only part of an unchanged postgres volume (2026-10-12);
	// the root summary is the whole tree.
	partial := withSummary(snap("s", 2, 40, 4), 100, 10)
	shrunk := withSummary(snap("s", 2, 100, 10), 40, 4)
	for _, tc := range []struct {
		name    string
		s, prev Snapshot
		hasPrev bool
		fail    bool
	}{
		{"fine", snap("s", 2, 100, 10), prev, true, false},
		{"partial stats, whole tree as before", partial, prev, true, false},
		{"whole tree under half", shrunk, prev, true, true},
		{"first", snap("s", 2, 5, 1), Snapshot{}, false, false},
		{"first empty", snap("s", 2, 0, 0), Snapshot{}, false, false},
		{"errors", errs, prev, true, true},
		{"ignored errors", ignored, prev, true, true},
		{"empty after non-empty", snap("s", 2, 0, 0), prev, true, true},
		{"empty after empty", snap("s", 2, 0, 0), empty, true, false},
		{"half the size", snap("s", 2, 50, 10), prev, true, false},
		{"under half the size", snap("s", 2, 49, 10), prev, true, true},
		{"under half the files", snap("s", 2, 100, 4), prev, true, true},
		{"grew", snap("s", 2, 1000, 100), prev, true, false},
	} {
		var p *Snapshot
		if tc.hasPrev {
			p = &tc.prev
		}
		if err := checkSnapshot(&tc.s, p); (err != nil) != tc.fail {
			t.Errorf("%s: error %v, want failure %v", tc.name, err, tc.fail)
		}
	}
}

func TestVerify(t *testing.T) {
	f := &cmdrun.Fake{}
	f.On("kopia repository status", "")
	list := []Snapshot{snap("old", 1, 1000, 100), snap("prev", 2, 100, 10), snap("new", 3, 40, 10)}
	b, _ := json.Marshal(list)
	f.On("kopia snapshot list --json root@ceph-backup:/run/ceph-backup/a/b", string(b))
	f.On("kopia snapshot verify", "")
	a := newActs(t, f)

	err := a.Verify(context.Background(), VerifyInput{BackupResult{"new", "/run/ceph-backup/a/b"}, 10})
	if err == nil || !strings.Contains(err.Error(), "under half") || !strings.Contains(err.Error(), "prev") {
		t.Errorf("shrunk snapshot: %v", err)
	}
	if f.Called("kopia snapshot verify") {
		t.Error("read back a snapshot that failed its checks")
	}
	if err := a.Verify(context.Background(), VerifyInput{BackupResult{"prev", "/run/ceph-backup/a/b"}, 100}); err == nil {
		t.Error("prev is a tenth of old, should fail")
	}
	list[2].Stats.TotalSize = 90
	b, _ = json.Marshal(list)
	f.On("kopia snapshot list --json root@ceph-backup:/run/ceph-backup/a/b", string(b))
	if err := a.Verify(context.Background(), VerifyInput{BackupResult{"new", "/run/ceph-backup/a/b"}, 10}); err != nil {
		t.Fatal(err)
	}
	if !f.Called("kopia snapshot verify --no-progress --verify-files-percent=10 new") {
		t.Errorf("calls %q", f.Calls)
	}
}

func TestSyncAndVerifyStorj(t *testing.T) {
	f := &cmdrun.Fake{}
	a := newActs(t, f)
	var syncEnv []string
	f.OnFunc("kopia repository", func(c cmdrun.Cmd) (string, error) {
		if c.Args[1] == "sync-to" {
			syncEnv = c.Env
		}
		return "", nil
	})
	if err := a.SyncStorj(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.Called("kopia repository sync-to rclone --no-progress --remote-path=storj:ceph-backup --flat --delete --parallel=8") {
		t.Errorf("calls %q", f.Calls)
	}
	if !contains(syncEnv, "RCLONE_CONFIG_STORJ_ACCESS_KEY_ID=SK1") || !contains(syncEnv, "RCLONE_CONFIG_STORJ_ENDPOINT=https://gateway.example") || !contains(syncEnv, "KOPIA_PASSWORD=pw") {
		t.Errorf("sync env %q", syncEnv)
	}

	garageSnaps, _ := json.Marshal([]Snapshot{snap("a1", 1, 1, 1), snap("a2", 2, 1, 1)})
	storjSnaps, _ := json.Marshal([]Snapshot{snap("a1", 1, 1, 1)})
	f.OnFunc("kopia snapshot list --json --all", func(c cmdrun.Cmd) (string, error) {
		for _, e := range c.Env {
			if strings.HasSuffix(e, "storj.config") {
				return string(storjSnaps), nil
			}
		}
		return string(garageSnaps), nil
	})
	var storjListEnv []string
	f.OnFunc("kopia snapshot list --json --all", func(c cmdrun.Cmd) (string, error) {
		for _, e := range c.Env {
			if strings.HasSuffix(e, "storj.config") {
				storjListEnv = c.Env
				return string(storjSnaps), nil
			}
		}
		return string(garageSnaps), nil
	})
	f.On("kopia snapshot verify", "")
	if err := a.VerifyStorj(context.Background()); err == nil || !strings.Contains(err.Error(), "a2") {
		t.Errorf("missing newest snapshot: %v", err)
	}
	storjSnaps = garageSnaps
	if err := a.VerifyStorj(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.Called("kopia snapshot verify --no-progress --verify-files-percent=1 a2") {
		t.Errorf("calls %q", f.Calls)
	}
	// kopia starts rclone for every command against the copy.
	if !contains(storjListEnv, "RCLONE_CONFIG_STORJ_ACCESS_KEY_ID=SK1") {
		t.Errorf("storj snapshot list env %q", storjListEnv)
	}
}

func TestConnect(t *testing.T) {
	f := &cmdrun.Fake{}
	f.Fail("kopia repository status", "not connected")
	var env []string
	f.OnFunc("kopia repository connect", func(c cmdrun.Cmd) (string, error) { env = c.Env; return "", nil })
	a := newActs(t, f)
	if err := a.connect(context.Background(), storj); err != nil {
		t.Fatal(err)
	}
	if !f.Called("kopia repository connect rclone --remote-path=storj:ceph-backup --flat --readonly --override-hostname=ceph-backup --override-username=root") {
		t.Errorf("calls %q", f.Calls)
	}
	if !contains(env, "RCLONE_CONFIG_STORJ_SECRET_ACCESS_KEY=ss") || !contains(env, "KOPIA_CONFIG_PATH="+a.Cfg.RunDir+"/storj.config") {
		t.Errorf("env %q", env)
	}
	if err := a.connect(context.Background(), garage); err != nil {
		t.Fatal(err)
	}
	if !f.Called("kopia repository connect s3 --bucket=ceph-backup --endpoint=garage.example --region=garage --override-hostname=ceph-backup --override-username=root") {
		t.Errorf("calls %q", f.Calls)
	}
}

func TestAdminOps(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		switch {
		case r.URL.Path == "/admin/metadata":
			w.Write([]byte(`["user","bucket"]`))
		case r.URL.Path == "/admin/metadata/user" && r.URL.Query().Get("key") == "":
			w.Write([]byte(`["u1"]`))
		case r.URL.Path == "/admin/metadata/user":
			w.Write([]byte(`{"key":"` + r.URL.Query().Get("key") + `"}`))
		default:
			http.Error(w, "no", http.StatusForbidden)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	os.WriteFile(dir+"/access_key", []byte("AKADMIN\n"), 0o600)
	os.WriteFile(dir+"/secret_key", []byte("s"), 0o600)
	c := &AdminOps{Endpoint: srv.URL, KeyDir: dir}
	ctx := context.Background()
	if s, err := c.MetadataSections(ctx); err != nil || !reflect.DeepEqual(s, []string{"user", "bucket"}) {
		t.Errorf("sections %q %v", s, err)
	}
	if k, err := c.MetadataKeys(ctx, "user"); err != nil || !reflect.DeepEqual(k, []string{"u1"}) {
		t.Errorf("keys %q %v", k, err)
	}
	if b, err := c.MetadataGet(ctx, "user", "RGW1$app"); err != nil || string(b) != `{"key":"RGW1$app"}` {
		t.Errorf("get %s %v", b, err)
	}
	if _, err := c.MetadataKeys(ctx, "otp"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("forbidden: %v", err)
	}
	for _, h := range auth {
		if !strings.HasPrefix(h, "AWS4-HMAC-SHA256 Credential=AKADMIN/") || !strings.Contains(h, "/default/s3/aws4_request") {
			t.Errorf("authorization %q", h)
		}
	}
}
