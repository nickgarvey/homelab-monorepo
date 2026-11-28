package bot

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TokenSource gives GitHub installation tokens (ghapp.App).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Identity is the author and committer of the bot's commits.
type Identity struct {
	Name  string
	Email string
}

// Git works in one clone under the state directory, with a worktree per job
// ("update" for UpdateRun, "dev" for DevBuild), so the two never share a
// checkout.
type Git struct {
	Run       Runner
	Bin       string // path to git
	Dir       string // the clone: <state>/repo
	RemoteURL string // https://github.com/<owner>/<repo>.git
	GitHub    TokenSource
	Committer Identity
}

// env passes the installation token as an HTTP header through git's config
// environment, so it is in no command line and no file.
func (g *Git) env(ctx context.Context) ([]string, error) {
	tok, err := g.GitHub.Token(ctx)
	if err != nil {
		return nil, err
	}
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + tok))
	return []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + basic,
		"GIT_AUTHOR_NAME=" + g.Committer.Name,
		"GIT_AUTHOR_EMAIL=" + g.Committer.Email,
		"GIT_COMMITTER_NAME=" + g.Committer.Name,
		"GIT_COMMITTER_EMAIL=" + g.Committer.Email,
	}, nil
}

// git runs git in dir (the clone when dir is "") with the bot's identity
// and token.
func (g *Git) git(ctx context.Context, dir string, args ...string) (string, error) {
	env, err := g.env(ctx)
	if err != nil {
		return "", err
	}
	if dir == "" {
		dir = g.Dir
	}
	out, err := g.Run.Run(ctx, Cmd{Dir: dir, Name: g.Bin, Args: args, Env: env})
	return strings.TrimSpace(string(out)), err
}

// Worktree returns the path of the named worktree.
func (g *Git) Worktree(name string) string {
	return filepath.Join(filepath.Dir(g.Dir), "work", name)
}

// Fetch makes the clone if it is missing and fetches every branch of origin.
func (g *Git) Fetch(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(g.Dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(g.Dir, 0o755); err != nil {
			return err
		}
		if _, err := g.git(ctx, "", "init", "--quiet"); err != nil {
			return err
		}
		if _, err := g.git(ctx, "", "remote", "add", "origin", g.RemoteURL); err != nil {
			return err
		}
	}
	_, err := g.git(ctx, "", "fetch", "--quiet", "--prune", "origin", "+refs/heads/*:refs/remotes/origin/*")
	return err
}

// Checkout puts the named worktree at rev, detached and clean, creating the
// worktree if needed. branch, if set, is (re)created there at rev.
func (g *Git) Checkout(ctx context.Context, name, rev, branch string) (string, error) {
	wt := g.Worktree(name)
	if _, err := os.Stat(filepath.Join(wt, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := g.git(ctx, "", "worktree", "prune"); err != nil {
			return "", err
		}
		if _, err := g.git(ctx, "", "worktree", "add", "--quiet", "--detach", wt, rev); err != nil {
			return "", err
		}
	}
	// A cherry-pick left half done by a killed worker would block checkout.
	_, _ = g.git(ctx, wt, "cherry-pick", "--abort")
	args := []string{"checkout", "--quiet", "--force", "--detach", rev}
	if branch != "" {
		args = []string{"checkout", "--quiet", "--force", "-B", branch, rev}
	}
	if _, err := g.git(ctx, wt, args...); err != nil {
		return "", err
	}
	if _, err := g.git(ctx, wt, "clean", "--quiet", "-fdx"); err != nil {
		return "", err
	}
	return wt, nil
}

// RevParse returns the commit of ref, or "" if it does not exist.
func (g *Git) RevParse(ctx context.Context, dir, ref string) (string, error) {
	out, err := g.git(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		if out == "" {
			return "", nil
		}
		return "", err
	}
	return out, nil
}

// Commit is one commit, as carried forward on flake-update.
type Commit struct {
	SHA     string
	Subject string
}

// Patches returns the commits on branch that are not on base, oldest first,
// leaving out the bot's own lock updates.
func (g *Git) Patches(ctx context.Context, base, branch string) ([]Commit, error) {
	out, err := g.git(ctx, "", "log", "--reverse", "--no-merges", "--format=%H%x00%s%x00%B%x1e", base+".."+branch)
	if err != nil {
		return nil, err
	}
	return parsePatches(out), nil
}

func parsePatches(out string) []Commit {
	var res []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		parts := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		if hasLine(parts[2], LockTrailer) {
			continue
		}
		res = append(res, Commit{SHA: parts[0], Subject: parts[1]})
	}
	return res
}

func hasLine(body, line string) bool {
	for _, l := range strings.Split(body, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// ErrConflict is a carried patch that no longer applies.
var ErrConflict = errors.New("patch does not apply")

// CherryPick applies c on the worktree's HEAD. It reports whether c added
// anything: a patch whose change dev already has becomes empty and is
// dropped. On a conflict the pick is aborted and ErrConflict returned.
func (g *Git) CherryPick(ctx context.Context, wt string, c Commit) (bool, error) {
	before, err := g.RevParse(ctx, wt, "HEAD")
	if err != nil {
		return false, err
	}
	if _, err := g.git(ctx, wt, "cherry-pick", "--empty=drop", c.SHA); err != nil {
		_, _ = g.git(ctx, wt, "cherry-pick", "--abort")
		return false, fmt.Errorf("%w: %s %s: %v", ErrConflict, short(c.SHA), c.Subject, err)
	}
	after, err := g.RevParse(ctx, wt, "HEAD")
	if err != nil {
		return false, err
	}
	return after != before, nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
