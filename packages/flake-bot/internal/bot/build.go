package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Kind is what a target is: a host's system, or a flake check.
type Kind string

const (
	KindHost  Kind = "host"
	KindCheck Kind = "check"
)

// Target is one thing the bot builds.
type Target struct {
	Kind Kind
	Name string
}

// Installable is the target's flake attribute.
func (t Target) Installable(system string) string {
	if t.Kind == KindHost {
		return ".#nixosConfigurations." + t.Name + ".config.system.build.toplevel"
	}
	return ".#checks." + system + "." + t.Name
}

// TargetResult is how one target fared.
type TargetResult struct {
	Kind    Kind
	Name    string
	DrvPath string `json:",omitempty"`
	OutPath string `json:",omitempty"`
	// Error is why it failed: its evaluation's error, or "build failed"
	// (the build log is BuildResult.LogFile).
	Error string `json:",omitempty"`
}

func (t TargetResult) OK() bool { return t.Error == "" && t.OutPath != "" }

// BuildResult is a build of every target at Rev.
type BuildResult struct {
	Rev     string
	Targets []TargetResult
	// LogFile is the whole nix build log on the worker's host; LogTail its end.
	LogFile string `json:",omitempty"`
	LogTail string `json:",omitempty"`
}

// OK reports whether every target built.
func (b BuildResult) OK() bool {
	for _, t := range b.Targets {
		if !t.OK() {
			return false
		}
	}
	return len(b.Targets) > 0
}

// Built returns the out paths of the targets that built.
func (b BuildResult) Built() []string {
	var paths []string
	for _, t := range b.Targets {
		if t.OK() {
			paths = append(paths, t.OutPath)
		}
	}
	return paths
}

// Failed returns the targets that did not build.
func (b BuildResult) Failed() []TargetResult {
	var res []TargetResult
	for _, t := range b.Targets {
		if !t.OK() {
			res = append(res, t)
		}
	}
	return res
}

// Nix evaluates and builds the flake in a worktree.
type Nix struct {
	Run    Runner
	Bin    string // path to nix
	System string // x86_64-linux: whose checks to build
	// EvalJobs is how many targets are evaluated at once.
	EvalJobs int
	// Env is added to every nix command.
	Env []string
	// GitHub, if set, gives flake update a token for GitHub's API, whose
	// unauthenticated limit the router's NAT64 address shares.
	GitHub TokenSource
	LogDir string
}

// FlakeUpdate runs `nix flake update` in dir.
func (n *Nix) FlakeUpdate(ctx context.Context, dir string) error {
	env := n.Env
	if n.GitHub != nil {
		tok, err := n.GitHub.Token(ctx)
		if err != nil {
			return err
		}
		env = append(append([]string{}, env...), "NIX_CONFIG=access-tokens = github.com="+tok)
	}
	_, err := n.Run.Run(ctx, Cmd{Dir: dir, Name: n.Bin, Args: []string{"flake", "update"}, Env: env})
	return err
}

func (n *Nix) nix(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return n.Run.Run(ctx, Cmd{Dir: dir, Name: n.Bin, Args: args, Env: n.Env})
}

// Targets lists every nixosConfiguration and every check for n.System.
func (n *Nix) Targets(ctx context.Context, dir string) ([]Target, error) {
	var targets []Target
	for _, q := range []struct {
		kind Kind
		attr string
	}{{KindHost, ".#nixosConfigurations"}, {KindCheck, ".#checks." + n.System}} {
		out, err := n.nix(ctx, dir, "eval", "--json", q.attr, "--apply", "builtins.attrNames")
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", q.attr, err)
		}
		var names []string
		if err := json.Unmarshal(out, &names); err != nil {
			return nil, fmt.Errorf("list %s: %w", q.attr, err)
		}
		sort.Strings(names)
		for _, name := range names {
			targets = append(targets, Target{Kind: q.kind, Name: name})
		}
	}
	return targets, nil
}

// Build evaluates each target on its own, so one that fails to evaluate
// does not hide the others, then builds all that evaluated in one
// `nix build --keep-going`. heartbeat is called as work progresses.
func (n *Nix) Build(ctx context.Context, dir, rev string, heartbeat func(string)) (BuildResult, error) {
	targets, err := n.Targets(ctx, dir)
	if err != nil {
		return BuildResult{}, err
	}
	res := BuildResult{Rev: rev, Targets: make([]TargetResult, len(targets))}
	outs := make([]string, len(targets))

	jobs := max(n.EvalJobs, 1)
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i, t := range targets {
		res.Targets[i] = TargetResult{Kind: t.Kind, Name: t.Name}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, err := n.nix(ctx, dir, "eval", "--json", t.Installable(n.System),
				"--apply", "d: { drv = d.drvPath; out = d.outPath; }")
			if err != nil {
				res.Targets[i].Error = "eval: " + Tail(err.Error(), 30)
				return
			}
			// Not drvPath/outPath: nix prints a set with an outPath as that string.
			var p struct {
				DrvPath string `json:"drv"`
				OutPath string `json:"out"`
			}
			if err := json.Unmarshal(out, &p); err != nil || p.DrvPath == "" || p.OutPath == "" {
				res.Targets[i].Error = fmt.Sprintf("eval: unexpected output %q", Tail(string(out), 3))
				return
			}
			res.Targets[i].DrvPath, outs[i] = p.DrvPath, p.OutPath
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	heartbeat("evaluated")

	var installables []string
	for _, t := range res.Targets {
		if t.DrvPath != "" {
			installables = append(installables, t.DrvPath+"^out")
		}
	}
	if len(installables) > 0 {
		if err := os.MkdirAll(n.LogDir, 0o755); err != nil {
			return res, err
		}
		res.LogFile = filepath.Join(n.LogDir, fmt.Sprintf("build-%s.log", short(rev)))
		f, err := os.Create(res.LogFile)
		if err != nil {
			return res, err
		}
		args := append([]string{"build", "--keep-going", "--no-link", "--print-build-logs"}, installables...)
		_, buildErr := n.Run.Run(ctx, Cmd{Dir: dir, Name: n.Bin, Args: args, Env: n.Env, Stderr: f})
		f.Close()
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if buildErr != nil {
			if log, err := os.ReadFile(res.LogFile); err == nil {
				res.LogTail = Tail(string(log), 150)
			}
		}
	}
	heartbeat("built")

	for i, t := range res.Targets {
		if t.DrvPath == "" {
			continue
		}
		path := outs[i]
		if _, err := n.Run.Run(ctx, Cmd{Name: n.Bin, Args: []string{"path-info", path}, Env: n.Env}); err != nil {
			res.Targets[i].Error = "build failed"
			continue
		}
		res.Targets[i].OutPath = path
	}
	return res, nil
}
