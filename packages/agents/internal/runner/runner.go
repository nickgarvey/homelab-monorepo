// Package runner runs external commands behind an interface, so code that
// shells out (openshell, skopeo, kubectl) can be tested against a fake.
package runner

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// Runner executes external commands.
type Runner interface {
	Run(ctx context.Context, argv []string, opts Opts) Result
}

type Opts struct {
	// Stdin, if set, is the command's standard input.
	Stdin []byte
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	// Err is set when the command could not be started. Nil on a normal exit,
	// even a non-zero one.
	Err error
}

func (r Result) Failed() bool { return r.Err != nil || r.ExitCode != 0 }

// Exec runs commands for real.
type Exec struct{}

func (Exec) Run(ctx context.Context, argv []string, opts Opts) Result {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if opts.Stdin != nil {
		cmd.Stdin = bytes.NewReader(opts.Stdin)
	}
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	case err != nil:
		res.Err = err
	}
	return res
}
