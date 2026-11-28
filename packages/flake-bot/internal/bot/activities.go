package bot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// Worktree names.
const (
	UpdateWorktree = "update"
	DevWorktree    = "dev"
)

// Branches on GitHub.
const (
	DevBranch    = "dev"
	UpdateBranch = "flake-update"
)

// Activities are the steps the workflows run, all on the worker's host
// (wabbajack), one at a time (the worker allows one activity), so builds
// never compete.
type Activities struct {
	Git   *Git
	Nix   *Nix
	Store Store
	// Celler pushes closures: celler push <Cache> <paths...>, with
	// XDG_CONFIG_HOME pointing at its client config.
	CellerBin        string
	CellerCache      string
	CellerConfigHome string
	Run              Runner
	// Now is time.Now, replaced in tests.
	Now func() time.Time
}

// PrepareResult is flake-update rebuilt in the update worktree.
type PrepareResult struct {
	Base string // origin/dev
	// OldBranch is the local flake-update before this run ("" if none).
	OldBranch string
	Head      string // the rebuilt flake-update
	// Inputs are the flake inputs `nix flake update` changed.
	Inputs []string
	// Patches are the carried commits that still add something; Dropped
	// those dev now has.
	Patches []Commit
	Dropped []Commit
	// Conflict is a carried patch that no longer applies; the patches after
	// it are not applied either, and Head is the last good state.
	Conflict string `json:",omitempty"`
}

// PrepareUpdate rebuilds flake-update as origin/dev, plus one fresh
// `nix flake update` commit, plus the patches flake-update carried.
//
// flake-update is a branch of the clone on this host only; it is never
// pushed. It carries the patches between runs while the update does not
// build; once it does, Land pushes its head to dev, patches included. The
// work is done detached and the branch moved only at the end, and not on a
// conflict, so a run that fails part way loses no patch.
func (a *Activities) PrepareUpdate(ctx context.Context) (PrepareResult, error) {
	g := a.Git
	if err := g.Fetch(ctx); err != nil {
		return PrepareResult{}, err
	}
	var r PrepareResult
	var err error
	if r.Base, err = g.RevParse(ctx, "", "refs/remotes/origin/"+DevBranch); err != nil || r.Base == "" {
		return r, fmt.Errorf("origin/%s: %v", DevBranch, err)
	}
	if r.OldBranch, err = g.RevParse(ctx, "", "refs/heads/"+UpdateBranch); err != nil {
		return r, err
	}
	var patches []Commit
	if r.OldBranch != "" {
		if patches, err = g.Patches(ctx, r.Base, r.OldBranch); err != nil {
			return r, err
		}
	}
	wt, err := g.Checkout(ctx, UpdateWorktree, r.Base, "")
	if err != nil {
		return r, err
	}

	before, err := os.ReadFile(filepath.Join(wt, "flake.lock"))
	if err != nil {
		return r, err
	}
	stop := heartbeatEvery(ctx, time.Minute)
	err = a.Nix.FlakeUpdate(ctx, wt)
	stop()
	if err != nil {
		return r, err
	}
	after, err := os.ReadFile(filepath.Join(wt, "flake.lock"))
	if err != nil {
		return r, err
	}
	if r.Inputs, err = ChangedInputs(before, after); err != nil {
		return r, err
	}
	if len(r.Inputs) > 0 {
		if _, err := g.git(ctx, wt, "commit", "--quiet", "-m", LockCommitMessage(r.Inputs), "--", "flake.lock"); err != nil {
			return r, err
		}
	}

	for _, p := range patches {
		added, err := g.CherryPick(ctx, wt, p)
		if errors.Is(err, ErrConflict) {
			r.Conflict = err.Error()
			break
		}
		if err != nil {
			return r, err
		}
		if added {
			r.Patches = append(r.Patches, p)
		} else {
			r.Dropped = append(r.Dropped, p)
		}
	}
	if r.Head, err = g.RevParse(ctx, wt, "HEAD"); err != nil {
		return r, err
	}
	// On a conflict the branch keeps every patch for whoever fixes it.
	if r.Conflict == "" {
		if _, err := g.git(ctx, "", "branch", "--force", UpdateBranch, r.Head); err != nil {
			return r, err
		}
	}
	return r, nil
}

// DevResult is origin/dev checked out in the dev worktree.
type DevResult struct {
	Rev     string
	Subject string
}

// PrepareDev checks out origin/dev in the dev worktree.
func (a *Activities) PrepareDev(ctx context.Context) (DevResult, error) {
	g := a.Git
	if err := g.Fetch(ctx); err != nil {
		return DevResult{}, err
	}
	rev, err := g.RevParse(ctx, "", "refs/remotes/origin/"+DevBranch)
	if err != nil || rev == "" {
		return DevResult{}, fmt.Errorf("origin/%s: %v", DevBranch, err)
	}
	wt, err := g.Checkout(ctx, DevWorktree, rev, "")
	if err != nil {
		return DevResult{}, err
	}
	subject, err := g.git(ctx, wt, "log", "-1", "--format=%s", rev)
	if err != nil {
		return DevResult{}, err
	}
	return DevResult{Rev: rev, Subject: subject}, nil
}

// BuildRequest names the worktree to build and the commit it must be at.
type BuildRequest struct {
	Worktree string
	Rev      string
}

// Build builds every target in the worktree, which must be at Rev.
func (a *Activities) Build(ctx context.Context, req BuildRequest) (BuildResult, error) {
	wt := a.Git.Worktree(req.Worktree)
	head, err := a.Git.RevParse(ctx, wt, "HEAD")
	if err != nil {
		return BuildResult{}, err
	}
	if head != req.Rev {
		// Another job moved the worktree: the workflow must prepare again.
		return BuildResult{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("worktree %s is at %s, not %s", req.Worktree, short(head), short(req.Rev)), "WorktreeMoved", nil)
	}
	stop := heartbeatEvery(ctx, time.Minute)
	defer stop()
	return a.Nix.Build(ctx, wt, req.Rev, func(stage string) { activity.RecordHeartbeat(ctx, stage) })
}

// PushCeller pushes the closures of paths to celler.
func (a *Activities) PushCeller(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	stop := heartbeatEvery(ctx, time.Minute)
	defer stop()
	_, err := a.Run.Run(ctx, Cmd{
		Name: a.CellerBin,
		Args: append([]string{"push", a.CellerCache}, paths...),
		Env:  []string{"XDG_CONFIG_HOME=" + a.CellerConfigHome},
	})
	return err
}

// LandRequest fast-forwards dev from Base to Head.
type LandRequest struct {
	Base string
	Head string
}

// LandResult says whether dev moved to Head.
type LandResult struct {
	Landed bool
	// Reason is why not.
	Reason string `json:",omitempty"`
}

// Land fast-forwards origin/dev to Head if it is still at Base. A plain
// push: GitHub refuses a non-fast-forward, and the ruleset forbids one.
func (a *Activities) Land(ctx context.Context, req LandRequest) (LandResult, error) {
	g := a.Git
	if req.Head == req.Base {
		return LandResult{Reason: "nothing to land"}, nil
	}
	out, err := g.git(ctx, "", "ls-remote", "origin", "refs/heads/"+DevBranch)
	if err != nil {
		return LandResult{}, err
	}
	if cur := firstField(out); cur != req.Base {
		return LandResult{Reason: fmt.Sprintf("dev moved to %s during the run", short(cur))}, nil
	}
	if _, err := g.git(ctx, g.Worktree(UpdateWorktree), "push", "--quiet", "origin", req.Head+":refs/heads/"+DevBranch); err != nil {
		return LandResult{}, err
	}
	return LandResult{Landed: true}, nil
}

// ReadManifest returns the published manifest.
func (a *Activities) ReadManifest(ctx context.Context) (Manifest, error) {
	return a.Store.Read(ctx)
}

// PublishRequest is a dev build to merge into the manifest.
type PublishRequest struct {
	Dev   DevResult
	Build BuildResult
}

// Publish merges a dev build into the manifest and uploads it.
func (a *Activities) Publish(ctx context.Context, req PublishRequest) (Manifest, error) {
	m, err := a.Store.Read(ctx)
	if err != nil {
		return Manifest{}, err
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	m = m.Merge(req.Dev.Rev, req.Dev.Subject, now().UTC(), req.Build)
	return m, a.Store.Write(ctx, m)
}

func firstField(s string) string {
	for i, c := range s {
		if c == '\t' || c == ' ' || c == '\n' {
			return s[:i]
		}
	}
	return s
}

// heartbeatEvery heartbeats until stop is called, so a long command keeps
// its activity alive between the steps that report progress themselves.
func heartbeatEvery(ctx context.Context, d time.Duration) (stop func()) {
	if !activity.IsActivity(ctx) {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				activity.RecordHeartbeat(ctx)
			}
		}
	}()
	return func() { close(done) }
}
