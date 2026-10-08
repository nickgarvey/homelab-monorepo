// Package gateway is the worker's narrow view of the OpenShell gateway: the
// sandbox calls the session activities make, in one workspace. Gateway is
// implemented over the OpenShell Go SDK by Client, and in memory by Fake.
package gateway

import (
	"context"
	"errors"
)

// Phases a sandbox reports, besides Provisioning and Unknown.
const (
	PhaseReady    = "Ready"
	PhaseStarting = "Starting"
	PhaseStopping = "Stopping"
	PhaseStopped  = "Stopped"
	PhaseError    = "Error"
	PhaseDeleting = "Deleting"
)

var (
	// ErrNotFound: the sandbox does not exist.
	ErrNotFound = errors.New("sandbox not found")
	// ErrLost: the sandbox exists but can never become ready again: its main
	// process exited (phase Error) or it is being deleted. Exec is refused.
	ErrLost = errors.New("sandbox lost")
	// ErrNotReady: the sandbox exists and may become ready again (it is
	// stopped, or starting), but cannot run anything now.
	ErrNotReady = errors.New("sandbox not ready")
)

// Spec is what a sandbox is created with, from its agent type.
type Spec struct {
	Image   string
	Command []string
	// Policy is the type's sandbox policy, the policy YAML as JSON.
	Policy    map[string]any
	Providers []string
	Env       map[string]string
	// CPU and Memory are Kubernetes quantities, set as limits when not nil.
	CPU    *string
	Memory *string
	Labels map[string]string
}

type Sandbox struct {
	Name   string
	Phase  string
	Labels map[string]string
}

type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Gateway is the sandbox API of one workspace. Every call is safe to repeat:
// creating a sandbox that exists, or stopping or deleting one that is already
// stopped or gone, succeeds.
type Gateway interface {
	Create(ctx context.Context, name string, spec Spec) error
	// Get returns ErrNotFound for a missing sandbox.
	Get(ctx context.Context, name string) (*Sandbox, error)
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string) error
	Delete(ctx context.Context, name string) error
	// List returns the sandboxes whose labels match selector (k=v[,k=v]).
	List(ctx context.Context, selector string) ([]Sandbox, error)
	// WaitReady waits for phase Ready. It returns ErrNotFound or ErrLost
	// when the sandbox cannot get there, and the context's error on timeout.
	WaitReady(ctx context.Context, name string) error
	// Exec runs argv in the sandbox with stdin closed. A non-zero exit is
	// a result, not an error. It returns ErrNotFound, ErrLost or ErrNotReady
	// when the sandbox is gone or not ready.
	Exec(ctx context.Context, name string, argv []string) (ExecResult, error)
}
