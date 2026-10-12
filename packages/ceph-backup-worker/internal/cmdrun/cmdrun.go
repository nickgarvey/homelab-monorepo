// Package cmdrun runs the CLIs the activities shell out to (rbd, ceph, rados,
// mount, rclone, kopia). Exec is the real runner; the tests use a fake.
package cmdrun

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Cmd is one command line. Env is added to the worker's environment, from
// which every AWS_* variable is removed first: the worker's own STS settings
// are not meant for kopia or rclone, which get their credentials explicitly.
type Cmd struct {
	Name string
	Args []string
	Env  []string
}

func (c Cmd) String() string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

// Runner runs commands.
type Runner interface {
	// Run runs the command to completion and returns its stdout. A failure
	// carries the end of its stderr, so the Temporal UI shows why.
	Run(ctx context.Context, c Cmd) ([]byte, error)
	// Start starts a long-running command, such as an rclone mount.
	Start(ctx context.Context, c Cmd) (Process, error)
}

// Process is a started command.
type Process interface {
	// Wait waits for it to exit.
	Wait() error
	// Kill kills it.
	Kill()
}

// Exec runs commands with os/exec.
type Exec struct{}

// stderrTail is how much of a failed command's stderr goes into its error.
const stderrTail = 4096

func command(ctx context.Context, c Cmd) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "AWS_") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, c.Env...)
	return cmd
}

func (Exec) Run(ctx context.Context, c Cmd) ([]byte, error) {
	cmd := command(ctx, c)
	var stdout bytes.Buffer
	stderr := &tailBuffer{max: stderrTail}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%s: %w: %s", c.Name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (Exec) Start(ctx context.Context, c Cmd) (Process, error) {
	cmd := command(ctx, c)
	stderr := &tailBuffer{max: stderrTail}
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", c.Name, err)
	}
	return &process{cmd: cmd, stderr: stderr}, nil
}

type process struct {
	cmd    *exec.Cmd
	stderr *tailBuffer
}

func (p *process) Wait() error {
	if err := p.cmd.Wait(); err != nil {
		return fmt.Errorf("%s: %w: %s", p.cmd.Path, err, strings.TrimSpace(p.stderr.String()))
	}
	return nil
}

func (p *process) Kill() { _ = p.cmd.Process.Kill() }

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
