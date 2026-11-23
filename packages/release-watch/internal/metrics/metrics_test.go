package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"homelab/release-watch/internal/watch"
)

func TestCollector(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := New(reg)
	if n, err := testutil.GatherAndCount(reg); err != nil || n != 0 {
		t.Fatalf("before a run: %d series, %v", n, err)
	}

	calls := 0
	src := func(context.Context) (watch.Result, time.Time, bool, error) {
		calls++
		if calls > 1 {
			return watch.Result{}, time.Time{}, false, errors.New("temporal down")
		}
		return watch.Result{Targets: []watch.TargetResult{{
			Name: "authelia", Repo: "authelia/authelia", Running: "4.39.19", Latest: "v4.39.28", Behind: true,
			Fixes: []watch.Fix{{ID: "GHSA-1"}, {ID: "GHSA-2"}},
		}}}, time.Unix(1700000000, 0), true, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx, src, time.Millisecond); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	// The error after the first call keeps its values.
	want := `
# HELP homelab_release_watch_behind 1 if the running version is older than the newest GitHub release.
# TYPE homelab_release_watch_behind gauge
homelab_release_watch_behind{name="authelia",repo="authelia/authelia"} 1
# HELP homelab_release_watch_info 1, labelled with the version running in the cluster and the newest GitHub release.
# TYPE homelab_release_watch_info gauge
homelab_release_watch_info{latest="v4.39.28",name="authelia",repo="authelia/authelia",running="4.39.19"} 1
# HELP homelab_release_watch_last_success_timestamp_seconds When the newest successful ReleaseWatch run closed.
# TYPE homelab_release_watch_last_success_timestamp_seconds gauge
homelab_release_watch_last_success_timestamp_seconds 1.7e+09
# HELP homelab_release_watch_security_fixes Published security advisories that affect the running version and are fixed in a release up to the newest.
# TYPE homelab_release_watch_security_fixes gauge
homelab_release_watch_security_fixes{name="authelia",repo="authelia/authelia"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want)); err != nil {
		t.Error(err)
	}

	// A newer run replaces the series, not adds to them.
	c.Set(watch.Result{Targets: []watch.TargetResult{{Name: "authelia", Repo: "authelia/authelia", Running: "4.39.28", Latest: "v4.39.28"}}}, time.Unix(1700086400, 0))
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP homelab_release_watch_info 1, labelled with the version running in the cluster and the newest GitHub release.
# TYPE homelab_release_watch_info gauge
homelab_release_watch_info{latest="v4.39.28",name="authelia",repo="authelia/authelia",running="4.39.28"} 1
`), "homelab_release_watch_info"); err != nil {
		t.Error(err)
	}
}
