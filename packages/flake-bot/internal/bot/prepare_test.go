package bot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// lockFor is a flake.lock whose nixpkgs is at rev.
func lockFor(rev string) string {
	return `{"root":"root","version":7,"nodes":{"root":{"inputs":{"nixpkgs":"nixpkgs"}},"nixpkgs":{"locked":{"rev":"` + rev + `"}}}}` + "\n"
}

// fakeFlakeUpdate stands in for nix: `flake update` writes a lock with
// nixpkgs at whatever *rev says.
func fakeFlakeUpdate(rev *string) Runner {
	return &fakeRunner{fn: func(c Cmd) (string, error) {
		if strings.Join(c.Args, " ") != "flake update" {
			return "", os.ErrInvalid
		}
		return "", os.WriteFile(filepath.Join(c.Dir, "flake.lock"), []byte(lockFor(*rev)), 0o644)
	}}
}

// TestPrepareUpdateKeepsBranchLocal runs PrepareUpdate with real git and a
// fake `nix flake update`: flake-update lives in the bot's clone and is never
// pushed; patches carry from run to run on it; a conflict leaves it as it was.
func TestPrepareUpdateKeepsBranchLocal(t *testing.T) {
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
	writeFile(t, filepath.Join(src, "flake.lock"), lockFor("n1"))
	writeFile(t, filepath.Join(src, "a"), "1\n")
	gitT(t, src, "add", ".")
	gitT(t, src, "commit", "--quiet", "-m", "base")
	gitT(t, src, "push", "--quiet", "origin", "HEAD:dev")

	rev := "n2"
	g := &Git{Run: ExecRunner{}, Bin: "git", Dir: filepath.Join(root, "state", "repo"), RemoteURL: remote,
		GitHub: staticToken("tok"), Committer: Identity{Name: "bot", Email: "bot@example"}}
	acts := &Activities{Git: g, Nix: &Nix{Run: fakeFlakeUpdate(&rev), Bin: "nix"}}
	ctx := context.Background()

	// Run 1: a lock update, nothing carried yet.
	r1, err := acts.PrepareUpdate(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"nixpkgs"}, r1.Inputs)
	require.Empty(t, r1.OldBranch)
	branch, _ := g.RevParse(ctx, "", "refs/heads/"+UpdateBranch)
	require.Equal(t, r1.Head, branch)
	require.Equal(t, "Flake update: nixpkgs", gitT(t, g.Dir, "log", "-1", "--format=%s", branch))

	// A fix is committed on the bot's local flake-update (as the fixer will).
	wt := g.Worktree(UpdateWorktree)
	gitT(t, wt, "checkout", "--quiet", UpdateBranch)
	writeFile(t, filepath.Join(wt, "a"), "fixed\n")
	gitT(t, wt, "commit", "--quiet", "-am", "fix a")

	// Run 2: a newer nixpkgs; the fix is carried onto the new lock commit,
	// the old lock commit is not.
	rev = "n3"
	r2, err := acts.PrepareUpdate(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"fix a"}, subjects(r2.Patches))
	require.Equal(t, "fix a\nFlake update: nixpkgs\nbase", gitT(t, g.Dir, "log", "--format=%s", UpdateBranch))

	// dev moves under the fix: run 3 conflicts and the branch keeps the fix.
	gitT(t, src, "pull", "--quiet", "origin", "dev")
	writeFile(t, filepath.Join(src, "a"), "dev's own\n")
	gitT(t, src, "commit", "--quiet", "-am", "dev changes a")
	gitT(t, src, "push", "--quiet", "origin", "HEAD:dev")
	before, _ := g.RevParse(ctx, "", "refs/heads/"+UpdateBranch)
	r3, err := acts.PrepareUpdate(ctx)
	require.NoError(t, err)
	require.Contains(t, r3.Conflict, "fix a")
	after, _ := g.RevParse(ctx, "", "refs/heads/"+UpdateBranch)
	require.Equal(t, before, after, "a conflict leaves flake-update alone")

	// Nothing but dev was ever on the remote.
	require.Equal(t, "refs/heads/dev", strings.Fields(gitT(t, root, "--git-dir", remote, "for-each-ref", "--format=%(refname)"))[0])
	require.Len(t, strings.Fields(gitT(t, root, "--git-dir", remote, "for-each-ref", "--format=%(refname)")), 1)
}

func subjects(cs []Commit) []string {
	var s []string
	for _, c := range cs {
		s = append(s, c.Subject)
	}
	return s
}
