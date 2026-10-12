package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"homelab/ceph-backup-worker/internal/cmdrun"
)

// The identity every snapshot is taken as. The overrides make snapshot
// sources independent of which node takes them, so history survives moving
// the job: the bash job on joor used the same.
const (
	kopiaHost = "ceph-backup"
	kopiaUser = "root"
)

// repo is one Kopia repository connection: the backup repository on Garage,
// or the read-only connection to its copy on Storj.
type repo int

const (
	garage repo = iota
	storj
)

func (r repo) String() string { return [...]string{"garage", "storj"}[r] }

// kopiaEnv is the environment of every kopia command against r, with
// extra appended.
func (a *Activities) kopiaEnv(r repo, extra ...string) ([]string, error) {
	pw, err := readSecret(a.Cfg.KopiaDir, "kopia_password")
	if err != nil {
		return nil, err
	}
	env := []string{
		"KOPIA_CONFIG_PATH=" + filepath.Join(a.Cfg.RunDir, r.String()+".config"),
		"KOPIA_CACHE_DIRECTORY=" + filepath.Join(a.Cfg.StateDir, "cache", r.String()),
		"KOPIA_LOG_DIR=" + filepath.Join(a.Cfg.RunDir, "logs"),
		"KOPIA_CHECK_FOR_UPDATES=false",
		"KOPIA_PASSWORD=" + pw,
	}
	return append(env, extra...), nil
}

// kopia runs a kopia command against r. Commands against the Storj copy get
// the rclone remote's credentials: kopia starts rclone for each of them.
func (a *Activities) kopia(ctx context.Context, r repo, args ...string) ([]byte, error) {
	var extra []string
	if r == storj {
		creds, err := a.storageEnv(storj)
		if err != nil {
			return nil, err
		}
		extra = creds
	}
	env, err := a.kopiaEnv(r, extra...)
	if err != nil {
		return nil, err
	}
	return a.Run.Run(ctx, cmdrun.Cmd{Name: "kopia", Args: args, Env: env})
}

// storageEnv is the credentials for r's storage: for Garage as kopia's s3
// flags read them, for Storj as the rclone remote "storj" (storjArgs).
func (a *Activities) storageEnv(r repo) ([]string, error) {
	dir, id, secret := a.Cfg.KopiaDir, "garage_key_id", "garage_secret_key"
	if r == storj {
		dir, id, secret = a.Cfg.StorjDir, "access_key", "secret_key"
	}
	k, err := readSecret(dir, id)
	if err != nil {
		return nil, err
	}
	s, err := readSecret(dir, secret)
	if err != nil {
		return nil, err
	}
	if r == storj {
		return []string{
			"RCLONE_CONFIG_STORJ_TYPE=s3",
			"RCLONE_CONFIG_STORJ_PROVIDER=Storj",
			"RCLONE_CONFIG_STORJ_ENDPOINT=https://" + a.Cfg.StorjEndpoint,
			"RCLONE_CONFIG_STORJ_ACCESS_KEY_ID=" + k,
			"RCLONE_CONFIG_STORJ_SECRET_ACCESS_KEY=" + s,
		}, nil
	}
	return []string{"AWS_ACCESS_KEY_ID=" + k, "AWS_SECRET_ACCESS_KEY=" + s}, nil
}

// storjArgs is the kopia storage flags of the Storj copy. Not kopia's s3
// storage: Storj projects with self-managed encryption list only prefixes
// ending in "/" (the gateway answers 501 NotImplemented), and kopia lists
// by blob-ID prefix. rclone lists the bucket and filters itself, through
// the hosted S3 gateway (the pods are IPv6-only; Storj's storage nodes are
// reached only by IPv4). --flat stores blobs as <id>.f at the top, the
// same objects rclone's native storj backend reads (docs/ceph-backup.md).
func (a *Activities) storjArgs() []string {
	return []string{"--remote-path=storj:" + a.Cfg.StorjBucket, "--flat"}
}

// connect connects r unless it already is. The Storj copy is connected
// read-only: nothing but SyncStorj writes to it.
func (a *Activities) connect(ctx context.Context, r repo) error {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if _, err := a.kopia(ctx, r, "repository", "status"); err == nil {
		return nil
	}
	creds, err := a.storageEnv(r)
	if err != nil {
		return err
	}
	env, err := a.kopiaEnv(r, creds...)
	if err != nil {
		return err
	}
	args := []string{"repository", "connect", "s3",
		"--bucket=" + a.Cfg.GarageBucket, "--endpoint=" + a.Cfg.GarageEndpoint, "--region=" + a.Cfg.GarageRegion}
	if r == storj {
		args = append(append([]string{"repository", "connect", "rclone"}, a.storjArgs()...), "--readonly")
	}
	args = append(args, "--override-hostname="+kopiaHost, "--override-username="+kopiaUser)
	if _, err := a.Run.Run(ctx, cmdrun.Cmd{Name: "kopia", Args: args, Env: env}); err != nil {
		return fmt.Errorf("connecting to the %s repository: %w", r, err)
	}
	return nil
}

// Snapshot is the part of a Kopia snapshot manifest the worker reads.
type Snapshot struct {
	ID     string `json:"id"`
	Source struct {
		Host     string `json:"host"`
		UserName string `json:"userName"`
		Path     string `json:"path"`
	} `json:"source"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
	Stats     struct {
		TotalSize         int64 `json:"totalSize"`
		FileCount         int64 `json:"fileCount"`
		ErrorCount        int64 `json:"errorCount"`
		IgnoredErrorCount int64 `json:"ignoredErrorCount"`
	} `json:"stats"`
	RootEntry *struct {
		// The whole tree. Stats counts only part of it: an incremental
		// snapshot of an unchanged postgres volume had half the files there.
		Summary *struct {
			Size   int64 `json:"size"`
			Files  int64 `json:"files"`
			Failed int64 `json:"numFailed"`
		} `json:"summ"`
	} `json:"rootEntry"`
	Tags map[string]string `json:"tags"`
}

// totals is the size and file count of the snapshot's whole tree.
func (s *Snapshot) totals() (size, files int64) {
	if s.RootEntry != nil && s.RootEntry.Summary != nil {
		return s.RootEntry.Summary.Size, s.RootEntry.Summary.Files
	}
	return s.Stats.TotalSize, s.Stats.FileCount
}

// ours reports whether the snapshot was taken as this job's identity.
func (s *Snapshot) ours() bool { return s.Source.Host == kopiaHost && s.Source.UserName == kopiaUser }

// snapshotCreate snapshots path with tags (key:value) and returns the
// manifest.
func (a *Activities) snapshotCreate(ctx context.Context, path string, tags ...string) (Snapshot, error) {
	args := []string{"snapshot", "create", path, "--json", "--no-progress"}
	if a.Cfg.KopiaParallel > 0 {
		args = append(args, "--parallel="+strconv.Itoa(a.Cfg.KopiaParallel))
	}
	for _, t := range tags {
		args = append(args, "--tags", t)
	}
	var s Snapshot
	out, err := a.kopiaLong(ctx, garage, args...)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(out, &s); err != nil {
		return s, fmt.Errorf("kopia snapshot create %s: %w", path, err)
	}
	if s.ID == "" {
		return s, fmt.Errorf("kopia snapshot create %s: no snapshot ID in its output", path)
	}
	return s, nil
}

// kopiaLong runs a kopia command that may take hours, heartbeating while it
// does.
func (a *Activities) kopiaLong(ctx context.Context, r repo, args ...string) ([]byte, error) {
	var out []byte
	err := a.heartbeating(ctx, func(ctx context.Context) error {
		var err error
		out, err = a.kopia(ctx, r, args...)
		return err
	})
	return out, err
}

// snapshots lists r's snapshots: of one source path, or all of them.
func (a *Activities) snapshots(ctx context.Context, r repo, path string) ([]Snapshot, error) {
	args := []string{"snapshot", "list", "--json"}
	if path == "" {
		args = append(args, "--all")
	} else {
		// As user@host:path, so it does not depend on this pod having the path.
		args = append(args, kopiaUser+"@"+kopiaHost+":"+path)
	}
	out, err := a.kopia(ctx, r, args...)
	if err != nil {
		return nil, err
	}
	var s []Snapshot
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("kopia snapshot list: %w", err)
	}
	return s, nil
}

// newestPerSource is the newest snapshot of each of this job's sources,
// sorted by path.
func newestPerSource(snaps []Snapshot) []Snapshot {
	newest := map[string]Snapshot{}
	for _, s := range snaps {
		if !s.ours() {
			continue
		}
		if n, ok := newest[s.Source.Path]; !ok || s.StartTime.After(n.StartTime) {
			newest[s.Source.Path] = s
		}
	}
	out := make([]Snapshot, 0, len(newest))
	for _, s := range newest {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source.Path < out[j].Source.Path })
	return out
}

func ids(snaps []Snapshot) []string {
	out := make([]string, len(snaps))
	for i, s := range snaps {
		out[i] = s.ID
	}
	return out
}

// verifySnapshots reads back percent of the files of the given snapshots
// from r.
func (a *Activities) verifySnapshots(ctx context.Context, r repo, percent float64, snapIDs ...string) error {
	args := append([]string{"snapshot", "verify", "--no-progress",
		"--verify-files-percent=" + strconv.FormatFloat(percent, 'f', -1, 64)}, snapIDs...)
	_, err := a.kopiaLong(ctx, r, args...)
	return err
}
