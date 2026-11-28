package bot

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Cmd is one command the activities run: git, nix, celler or aws.
type Cmd struct {
	Dir  string
	Name string
	Args []string
	// Env is added to the worker's own environment.
	Env   []string
	Stdin []byte
	// Stderr, if set, also receives the command's stderr (a build log).
	Stderr io.Writer
}

// Runner runs commands; tests replace it with a fake.
type Runner interface {
	// Run returns the command's stdout. A failing command's error carries
	// the end of its stderr.
	Run(ctx context.Context, c Cmd) ([]byte, error)
}

// ExecRunner runs commands with os/exec.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, c Cmd) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	if c.Stdin != nil {
		cmd.Stdin = bytes.NewReader(c.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if c.Stderr != nil {
		cmd.Stderr = io.MultiWriter(&stderr, c.Stderr)
	}
	if err := cmd.Run(); err != nil {
		// Only the command name and its arguments: credentials travel in Env.
		return stdout.Bytes(), fmt.Errorf("%s %s: %w: %s", c.Name, strings.Join(c.Args, " "), err, Tail(stderr.String(), 20))
	}
	return stdout.Bytes(), nil
}

// Tail returns the last n lines of s.
func Tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
