package gateway

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
)

// TLSFiles are the worker's client certificate (CN=agents-worker,
// OU=openshell), which the gateway reads as its user, and the CA that signed
// the gateway's own certificate.
type TLSFiles struct {
	CertFile string
	KeyFile  string
	CAFile   string
}

// Client is Gateway over the OpenShell SDK. The SDK reads the certificate
// files once, when an SDK client is made, and cert-manager renews the
// certificate daily, so Client makes a new SDK client whenever the files'
// contents change.
type Client struct {
	Address   string
	Workspace string
	TLS       TLSFiles
	// PollInterval is how often WaitReady polls; one second if zero.
	PollInterval time.Duration

	mu     sync.Mutex
	sdk    *openshell.Client
	digest [32]byte
}

// client returns an SDK client made from the current certificate files.
func (c *Client) client() (*openshell.Client, error) {
	h := sha256.New()
	for _, f := range []string{c.TLS.CertFile, c.TLS.KeyFile, c.TLS.CAFile} {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("gateway credentials: %w", err)
		}
		h.Write(data)
		h.Write([]byte{0})
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sdk != nil && digest == c.digest {
		return c.sdk, nil
	}
	sdk, err := openshell.NewClient(openshell.Config{
		Address: c.Address,
		TLS: &openshell.TLSConfig{
			CertFile: c.TLS.CertFile,
			KeyFile:  c.TLS.KeyFile,
			CAFile:   c.TLS.CAFile,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("gateway client: %w", err)
	}
	// Calls in flight on the old client (a turn can run for many minutes)
	// finish on its connection before it is closed.
	if old := c.sdk; old != nil {
		time.AfterFunc(time.Hour, func() { old.Close() })
	}
	c.sdk, c.digest = sdk, digest
	return sdk, nil
}

func (c *Client) Create(ctx context.Context, name string, spec Spec) error {
	sdk, err := c.client()
	if err != nil {
		return err
	}
	policy, err := Policy(spec.Policy)
	if err != nil {
		return err
	}
	tmpl := &openshell.SandboxTemplate{Image: spec.Image}
	limits := map[string]any{}
	if spec.CPU != nil {
		limits["cpu"] = *spec.CPU
	}
	if spec.Memory != nil {
		limits["memory"] = *spec.Memory
	}
	if len(limits) > 0 {
		tmpl.Resources = map[string]any{"limits": limits}
	}
	_, err = sdk.Sandboxes().Create(ctx, c.Workspace, name, &openshell.SandboxSpec{
		Environment: spec.Env,
		Template:    tmpl,
		Providers:   spec.Providers,
		Policy:      policy,
		Command:     spec.Command,
	}, spec.Labels)
	if openshell.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (c *Client) Get(ctx context.Context, name string) (*Sandbox, error) {
	sdk, err := c.client()
	if err != nil {
		return nil, err
	}
	sb, err := sdk.Sandboxes().Get(ctx, c.Workspace, name)
	if openshell.IsNotFound(err) {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return &Sandbox{Name: sb.Name, Phase: string(sb.Status.Phase), Labels: sb.Labels}, nil
}

// Start and Stop of a sandbox already in (or on its way to) the asked-for
// state are refused by the gateway; those refusals are success here.
func (c *Client) Start(ctx context.Context, name string) error {
	sdk, err := c.client()
	if err != nil {
		return err
	}
	if _, err := sdk.Sandboxes().Start(ctx, c.Workspace, name); err != nil {
		if openshell.IsNotFound(err) {
			return fmt.Errorf("%s: %w", name, ErrNotFound)
		}
		if sb, getErr := c.Get(ctx, name); getErr == nil && (sb.Phase == PhaseReady || sb.Phase == PhaseStarting) {
			return nil
		}
		return err
	}
	return nil
}

func (c *Client) Stop(ctx context.Context, name string) error {
	sdk, err := c.client()
	if err != nil {
		return err
	}
	if _, err := sdk.Sandboxes().Stop(ctx, c.Workspace, name); err != nil {
		if openshell.IsNotFound(err) {
			return nil
		}
		if sb, getErr := c.Get(ctx, name); getErr == nil && (sb.Phase == PhaseStopped || sb.Phase == PhaseStopping) {
			return nil
		}
		return err
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, name string) error {
	sdk, err := c.client()
	if err != nil {
		return err
	}
	_, err = sdk.Sandboxes().Delete(ctx, c.Workspace, name, openshell.DeleteOptions{AllowMissing: true})
	return err
}

func (c *Client) List(ctx context.Context, selector string) ([]Sandbox, error) {
	sdk, err := c.client()
	if err != nil {
		return nil, err
	}
	sbs, err := sdk.Sandboxes().ListAll(ctx, c.Workspace, openshell.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	out := make([]Sandbox, 0, len(sbs))
	for _, sb := range sbs {
		out = append(out, Sandbox{Name: sb.Name, Phase: string(sb.Status.Phase), Labels: sb.Labels})
	}
	return out, nil
}

// WaitReady polls rather than using the SDK's WaitReady, which gives up on
// a sandbox that is still Stopped just after Start was called.
func (c *Client) WaitReady(ctx context.Context, name string) error {
	interval := c.PollInterval
	if interval == 0 {
		interval = time.Second
	}
	for {
		sb, err := c.Get(ctx, name)
		if err != nil {
			return err
		}
		switch sb.Phase {
		case PhaseReady:
			return nil
		case PhaseError, PhaseDeleting:
			return fmt.Errorf("%s: phase %s: %w", name, sb.Phase, ErrLost)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: waiting for Ready (phase %s): %w", name, sb.Phase, ctx.Err())
		case <-time.After(interval):
		}
	}
}

func (c *Client) Exec(ctx context.Context, name string, argv []string) (ExecResult, error) {
	sdk, err := c.client()
	if err != nil {
		return ExecResult{}, err
	}
	res, err := sdk.Exec().Run(ctx, c.Workspace, name, argv)
	if err != nil {
		if openshell.IsNotFound(err) {
			return ExecResult{}, fmt.Errorf("%s: %w", name, ErrNotFound)
		}
		// A sandbox that is not Ready refuses exec; say why.
		if sb, getErr := c.Get(ctx, name); getErr == nil && sb.Phase != PhaseReady {
			return ExecResult{}, notReady(name, sb.Phase, err)
		} else if errors.Is(getErr, ErrNotFound) {
			return ExecResult{}, getErr
		}
		return ExecResult{}, err
	}
	return ExecResult{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode}, nil
}

func notReady(name, phase string, cause error) error {
	if phase == PhaseError || phase == PhaseDeleting {
		return fmt.Errorf("%s: phase %s: %w (%v)", name, phase, ErrLost, cause)
	}
	return fmt.Errorf("%s: phase %s: %w (%v)", name, phase, ErrNotReady, cause)
}
