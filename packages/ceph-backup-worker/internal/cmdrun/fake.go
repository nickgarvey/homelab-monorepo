package cmdrun

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Fake is a Runner for tests. Each command line (Cmd.String) is answered by
// the first handler whose key it starts with, longest key first; a command
// with no handler fails. Calls records every command line, in order.
type Fake struct {
	mu       sync.Mutex
	handlers map[string]func(Cmd) (string, error)
	Calls    []string
	// Normalize, if set, rewrites each command line before it is matched
	// and recorded, such as to drop arguments every call has.
	Normalize func(string) string
}

// On answers commands starting with prefix with out.
func (f *Fake) On(prefix, out string) {
	f.OnFunc(prefix, func(Cmd) (string, error) { return out, nil })
}

// Fail makes commands starting with prefix fail with msg.
func (f *Fake) Fail(prefix, msg string) {
	f.OnFunc(prefix, func(Cmd) (string, error) { return "", fmt.Errorf("%s", msg) })
}

// OnFunc answers commands starting with prefix with h.
func (f *Fake) OnFunc(prefix string, h func(Cmd) (string, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handlers == nil {
		f.handlers = map[string]func(Cmd) (string, error){}
	}
	f.handlers[prefix] = h
}

func (f *Fake) Run(_ context.Context, c Cmd) ([]byte, error) {
	line := c.String()
	if f.Normalize != nil {
		line = f.Normalize(line)
	}
	f.mu.Lock()
	f.Calls = append(f.Calls, line)
	var best string
	var h func(Cmd) (string, error)
	for k, v := range f.handlers {
		if strings.HasPrefix(line, k) && len(k) >= len(best) {
			best, h = k, v
		}
	}
	f.mu.Unlock()
	if h == nil {
		return nil, fmt.Errorf("fake: unexpected command %q", line)
	}
	out, err := h(c)
	return []byte(out), err
}

// Start runs the command like Run; the process it returns has already
// exited.
func (f *Fake) Start(ctx context.Context, c Cmd) (Process, error) {
	if _, err := f.Run(ctx, c); err != nil {
		return nil, err
	}
	return fakeProcess{}, nil
}

// Called reports whether some command line started with prefix.
func (f *Fake) Called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

type fakeProcess struct{}

func (fakeProcess) Wait() error { return nil }
func (fakeProcess) Kill()       {}
