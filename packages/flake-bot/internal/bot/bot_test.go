package bot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChangedInputs(t *testing.T) {
	before := []byte(`{"root":"root","nodes":{"root":{"inputs":{}},
		"nixpkgs":{"locked":{"rev":"a","narHash":"x"}},
		"home-manager":{"locked":{"rev":"b"}},
		"gone":{"locked":{"rev":"c"}}}}`)
	after := []byte(`{"root":"root","nodes":{"root":{"inputs":{}},
		"nixpkgs":{"locked":{"narHash":"x","rev":"a"}},
		"home-manager":{"locked":{"rev":"b2"}},
		"new":{"locked":{"rev":"d"}}}}`)
	got, err := ChangedInputs(before, after)
	require.NoError(t, err)
	// nixpkgs only reordered its keys: unchanged.
	require.Equal(t, []string{"gone", "home-manager", "new"}, got)
}

func TestLockCommitMessage(t *testing.T) {
	msg := LockCommitMessage([]string{"nixpkgs", "pi-nix"})
	require.True(t, strings.HasPrefix(msg, "Flake update: nixpkgs, pi-nix\n\nHuman Text:\nNone\n\nLLM Text:\n"))
	require.True(t, hasLine(msg, LockTrailer))
	many := make([]string, 60)
	for i := range many {
		many[i] = fmt.Sprintf("input-%02d", i)
	}
	require.True(t, strings.HasPrefix(LockCommitMessage(many), "Flake update: 60 inputs\n"))
}

func TestParsePatches(t *testing.T) {
	out := "aaa\x00fix a\x00fix a\n\nbody\n\x1e\n" +
		"bbb\x00Flake update: x\x00Flake update: x\n\n" + LockTrailer + "\n\x1e\n" +
		"ccc\x00fix c\x00fix c\n\x1e\n"
	require.Equal(t, []Commit{{"aaa", "fix a"}, {"ccc", "fix c"}}, parsePatches(out))
}

func TestManifestMerge(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	m := Manifest{Commit: "old", Hosts: map[string]HostEntry{
		"talos":     {Path: "/nix/store/t0", Commit: "old", BuiltAt: t0},
		"dovahkiin": {Path: "/nix/store/d0", Commit: "old", BuiltAt: t0},
	}}
	b := BuildResult{Rev: "new", Targets: []TargetResult{
		{Kind: KindHost, Name: "talos", OutPath: "/nix/store/t1"},
		{Kind: KindHost, Name: "dovahkiin", Error: "build failed"},
		{Kind: KindCheck, Name: "deploy-tests", OutPath: "/nix/store/c1"},
	}}
	got := m.Merge("new", "subject", t1, b)
	require.Equal(t, "new", got.Commit)
	require.Equal(t, HostEntry{Path: "/nix/store/t1", Commit: "new", Subject: "subject", BuiltAt: t1}, got.Hosts["talos"])
	require.Equal(t, m.Hosts["dovahkiin"], got.Hosts["dovahkiin"], "a failed host keeps its last good system")
	require.NotContains(t, got.Hosts, "deploy-tests", "checks are not hosts")
	require.Equal(t, "/nix/store/t0", m.Hosts["talos"].Path, "Merge leaves the old manifest alone")
}

// fakeRunner answers commands by a function of their name and arguments.
type fakeRunner struct {
	mu    sync.Mutex
	calls []string
	fn    func(c Cmd) (string, error)
}

func (f *fakeRunner) Run(_ context.Context, c Cmd) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c.Name+" "+strings.Join(c.Args, " "))
	f.mu.Unlock()
	out, err := f.fn(c)
	return []byte(out), err
}

func TestNixBuild(t *testing.T) {
	f := &fakeRunner{fn: func(c Cmd) (string, error) {
		args := strings.Join(c.Args, " ")
		switch {
		case strings.HasPrefix(args, "eval --json .#nixosConfigurations --apply"):
			return `["broken","good","nobuild"]`, nil
		case strings.HasPrefix(args, "eval --json .#checks.x86_64-linux --apply"):
			return `["deploy-tests"]`, nil
		case strings.Contains(args, "nixosConfigurations.broken."):
			return "", fmt.Errorf("error: attribute missing")
		case strings.HasPrefix(args, "eval --json "):
			name := strings.Split(strings.TrimPrefix(c.Args[2], ".#"), ".")[1]
			if parts := strings.Split(c.Args[2], "."); strings.HasPrefix(c.Args[2], ".#checks") {
				name = parts[len(parts)-1]
			}
			return fmt.Sprintf(`{"drv":"/nix/store/%s.drv","out":"/nix/store/%s"}`, name, name), nil
		case strings.HasPrefix(args, "build "):
			require.Contains(t, args, "--keep-going")
			require.NotContains(t, args, "broken", "a target that failed to evaluate is not built")
			fmt.Fprintln(c.Stderr, "error: builder for nobuild failed")
			return "", fmt.Errorf("exit 1")
		case args == "path-info /nix/store/nobuild":
			return "", fmt.Errorf("not valid")
		case strings.HasPrefix(args, "path-info "):
			return "", nil
		}
		return "", fmt.Errorf("unexpected %s", args)
	}}
	n := &Nix{Run: f, Bin: "nix", System: "x86_64-linux", EvalJobs: 2, LogDir: t.TempDir()}
	res, err := n.Build(context.Background(), "/wt", "0123456789abcdef", func(string) {})
	require.NoError(t, err)
	byName := map[string]TargetResult{}
	for _, r := range res.Targets {
		byName[r.Name] = r
	}
	require.Contains(t, byName["broken"].Error, "eval: ")
	require.Equal(t, "build failed", byName["nobuild"].Error)
	require.True(t, byName["good"].OK())
	require.True(t, byName["deploy-tests"].OK())
	require.Equal(t, KindCheck, byName["deploy-tests"].Kind)
	require.False(t, res.OK())
	require.ElementsMatch(t, []string{"/nix/store/good", "/nix/store/deploy-tests"}, res.Built())
	require.Contains(t, res.LogTail, "builder for nobuild failed")
}

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

// gitT runs git in dir for the test, with a fixed identity.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// TestGitCarriesPatches runs the real git against a local "GitHub": the
// carried patches come forward onto a new dev, the one dev absorbed is
// dropped, the bot's old lock commit is not carried, and a patch that no
// longer applies is reported as a conflict with the worktree left clean.
func TestGitCarriesPatches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	src := filepath.Join(root, "src")
	gitT(t, root, "init", "--quiet", "--bare", "--initial-branch=dev", remote)
	gitT(t, root, "clone", "--quiet", remote, src)
	writeFile(t, filepath.Join(src, "a"), "1\n")
	writeFile(t, filepath.Join(src, "b"), "1\n")
	gitT(t, src, "add", ".")
	gitT(t, src, "commit", "--quiet", "-m", "base")
	gitT(t, src, "push", "--quiet", "origin", "HEAD:dev")

	// flake-update: the bot's lock commit, then two fixes.
	gitT(t, src, "checkout", "--quiet", "-b", "flake-update")
	writeFile(t, filepath.Join(src, "lock"), "x\n")
	gitT(t, src, "add", "lock")
	gitT(t, src, "commit", "--quiet", "-m", LockCommitMessage([]string{"nixpkgs"}))
	writeFile(t, filepath.Join(src, "a"), "fixed\n")
	gitT(t, src, "commit", "--quiet", "-am", "fix a")
	writeFile(t, filepath.Join(src, "b"), "fixed\n")
	gitT(t, src, "commit", "--quiet", "-am", "fix b")
	gitT(t, src, "push", "--quiet", "origin", "flake-update")
	// dev meanwhile gets fix b itself, and an unrelated commit.
	gitT(t, src, "checkout", "--quiet", "dev")
	writeFile(t, filepath.Join(src, "b"), "fixed\n")
	writeFile(t, filepath.Join(src, "c"), "new\n")
	gitT(t, src, "add", ".")
	gitT(t, src, "commit", "--quiet", "-m", "dev work, includes fix b")
	gitT(t, src, "push", "--quiet", "origin", "dev")

	g := &Git{Run: ExecRunner{}, Bin: "git", Dir: filepath.Join(root, "state", "repo"), RemoteURL: remote,
		GitHub: staticToken("tok"), Committer: Identity{Name: "bot", Email: "bot@example"}}
	ctx := context.Background()
	require.NoError(t, g.Fetch(ctx))
	base, err := g.RevParse(ctx, "", "refs/remotes/origin/dev")
	require.NoError(t, err)
	old, err := g.RevParse(ctx, "", "refs/remotes/origin/flake-update")
	require.NoError(t, err)
	missing, err := g.RevParse(ctx, "", "refs/remotes/origin/nope")
	require.NoError(t, err)
	require.Empty(t, missing)

	patches, err := g.Patches(ctx, base, old)
	require.NoError(t, err)
	require.Equal(t, []string{"fix a", "fix b"}, []string{patches[0].Subject, patches[1].Subject})

	wt, err := g.Checkout(ctx, UpdateWorktree, base, UpdateBranch)
	require.NoError(t, err)
	added, err := g.CherryPick(ctx, wt, patches[0])
	require.NoError(t, err)
	require.True(t, added)
	added, err = g.CherryPick(ctx, wt, patches[1])
	require.NoError(t, err)
	require.False(t, added, "dev already has fix b")
	content, _ := os.ReadFile(filepath.Join(wt, "a"))
	require.Equal(t, "fixed\n", string(content))
	require.Equal(t, "bot", gitT(t, wt, "log", "-1", "--format=%cn"))

	// A patch against a line dev has since changed differently conflicts.
	gitT(t, src, "checkout", "--quiet", "dev")
	writeFile(t, filepath.Join(src, "a"), "dev's own\n")
	gitT(t, src, "commit", "--quiet", "-am", "dev changes a")
	gitT(t, src, "push", "--quiet", "origin", "dev")
	require.NoError(t, g.Fetch(ctx))
	base2, _ := g.RevParse(ctx, "", "refs/remotes/origin/dev")
	wt, err = g.Checkout(ctx, UpdateWorktree, base2, UpdateBranch)
	require.NoError(t, err)
	_, err = g.CherryPick(ctx, wt, patches[0])
	require.ErrorIs(t, err, ErrConflict)
	require.Empty(t, gitT(t, wt, "status", "--porcelain"), "the conflict is aborted")
	head, _ := g.RevParse(ctx, wt, "HEAD")
	require.Equal(t, base2, head)
}
