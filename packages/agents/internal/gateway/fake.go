package gateway

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Fake is an in-memory Gateway for tests. Sandboxes change phase at once:
// Create and Start make them Ready, Stop makes them Stopped. Tests set a
// sandbox's phase directly to simulate a lost one, Stuck to simulate one that
// never comes up, and ExecFunc to play the programs inside.
type Fake struct {
	mu        sync.Mutex
	Sandboxes map[string]*FakeSandbox
	// Calls records each mutating call as "<method> <name>".
	Calls []string
	// ExecFunc answers Exec in a Ready sandbox; it returns exit 0 and no
	// output if nil.
	ExecFunc func(name string, argv []string) (ExecResult, error)
	// Fail, keyed "<method> <name>", makes that call return the error.
	Fail map[string]error
	// Stuck: Create and Start leave these sandboxes in phase Starting.
	Stuck map[string]bool
}

type FakeSandbox struct {
	Spec  Spec
	Phase string
}

func NewFake() *Fake {
	return &Fake{Sandboxes: map[string]*FakeSandbox{}, Fail: map[string]error{}, Stuck: map[string]bool{}}
}

func (f *Fake) call(method, name string) error {
	f.Calls = append(f.Calls, method+" "+name)
	return f.Fail[method+" "+name]
}

func (f *Fake) Create(_ context.Context, name string, spec Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("create", name); err != nil {
		return err
	}
	if _, err := Policy(spec.Policy); err != nil {
		return err
	}
	if _, ok := f.Sandboxes[name]; !ok {
		f.Sandboxes[name] = &FakeSandbox{Spec: spec, Phase: f.started(name)}
	}
	return nil
}

func (f *Fake) Get(_ context.Context, name string) (*Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sb, ok := f.Sandboxes[name]
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	return &Sandbox{Name: name, Phase: sb.Phase, Labels: sb.Spec.Labels}, nil
}

func (f *Fake) Start(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("start", name); err != nil {
		return err
	}
	sb, ok := f.Sandboxes[name]
	if !ok {
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	if sb.Phase == PhaseStopped {
		sb.Phase = f.started(name)
	}
	return nil
}

// started is the phase a created or started sandbox is in.
func (f *Fake) started(name string) string {
	if f.Stuck[name] {
		return PhaseStarting
	}
	return PhaseReady
}

func (f *Fake) Stop(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("stop", name); err != nil {
		return err
	}
	if sb, ok := f.Sandboxes[name]; ok && sb.Phase == PhaseReady {
		sb.Phase = PhaseStopped
	}
	return nil
}

func (f *Fake) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("delete", name); err != nil {
		return err
	}
	delete(f.Sandboxes, name)
	return nil
}

func (f *Fake) List(_ context.Context, selector string) ([]Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[string]string{}
	for _, kv := range strings.Split(selector, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			want[k] = v
		}
	}
	var out []Sandbox
	for name, sb := range f.Sandboxes {
		match := true
		for k, v := range want {
			if sb.Spec.Labels[k] != v {
				match = false
			}
		}
		if match {
			out = append(out, Sandbox{Name: name, Phase: sb.Phase, Labels: sb.Spec.Labels})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *Fake) WaitReady(ctx context.Context, name string) error {
	sb, err := f.Get(ctx, name)
	if err != nil {
		return err
	}
	switch sb.Phase {
	case PhaseReady:
		return nil
	case PhaseError, PhaseDeleting:
		return fmt.Errorf("%s: phase %s: %w", name, sb.Phase, ErrLost)
	}
	return fmt.Errorf("%s: phase %s: %w", name, sb.Phase, context.DeadlineExceeded)
}

func (f *Fake) Exec(ctx context.Context, name string, argv []string) (ExecResult, error) {
	f.mu.Lock()
	if err := f.call("exec", name); err != nil {
		f.mu.Unlock()
		return ExecResult{}, err
	}
	sb, ok := f.Sandboxes[name]
	f.mu.Unlock()
	if !ok {
		return ExecResult{}, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	if sb.Phase != PhaseReady {
		return ExecResult{}, notReady(name, sb.Phase, fmt.Errorf("sandbox is not ready (phase: %s)", sb.Phase))
	}
	if f.ExecFunc == nil {
		return ExecResult{}, nil
	}
	return f.ExecFunc(name, argv)
}

// SetPhase sets a sandbox's phase, as the gateway would on its own.
func (f *Fake) SetPhase(name, phase string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sandboxes[name].Phase = phase
}
