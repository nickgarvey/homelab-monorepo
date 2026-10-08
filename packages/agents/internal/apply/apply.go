// Package apply publishes a desired state to an OpenShell gateway and the
// cluster: it pushes images, creates the workspace and the worker's
// membership, imports or updates profiles, creates providers, and last writes
// the agents-desired-state ConfigMap the worker reads. It never deletes:
// profiles and providers that are not declared are reported.
package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"homelab/agents/internal/desired"
	"homelab/agents/internal/runner"
)

// ConfigMap is the name of the ConfigMap the worker reads, and the key the
// desired state is under.
const (
	ConfigMap    = "agents-desired-state"
	ConfigMapKey = "desired-state.json"
)

type Applier struct {
	Runner runner.Runner
	State  *desired.State
	// Namespace is the worker's Kubernetes namespace, which holds the ConfigMap.
	Namespace string
	// Subject is the worker's gateway user, made a member of the workspace.
	Subject string
	// DryRun reads everything but changes nothing; the actions are printed.
	DryRun bool
	Out    io.Writer

	// Actions lists what the run did (or, in a dry run, would do).
	Actions []string
	// Warnings lists what the run found but leaves alone.
	Warnings []string
}

// Run does every step in order and stops at the first error, so the
// ConfigMap is only written once everything it refers to is on the gateway.
func (a *Applier) Run(ctx context.Context) error {
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"preflight", a.preflight},
		{"images", a.images},
		{"workspace", a.workspace},
		{"profiles", a.profiles},
		{"providers", a.providers},
		{"desired state", a.configMap},
	}
	for _, s := range steps {
		if err := s.fn(ctx); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	if len(a.Actions) == 0 {
		a.say("Nothing to do.")
	}
	return nil
}

// openshell is an openshell command against the declared gateway.
func (a *Applier) openshell(args ...string) []string {
	return append([]string{"openshell", "-g", a.State.Gateway}, args...)
}

func (a *Applier) inWorkspace(args ...string) []string {
	return a.openshell(append([]string{"--workspace", a.State.Workspace}, args...)...)
}

func (a *Applier) run(ctx context.Context, argv []string, stdin []byte) runner.Result {
	return a.Runner.Run(ctx, argv, runner.Opts{Stdin: stdin})
}

// change runs a command that changes something, or only reports it in a dry run.
func (a *Applier) change(ctx context.Context, what string, argv []string, stdin []byte) error {
	a.Actions = append(a.Actions, what)
	if a.DryRun {
		a.say("Would %s", what)
		return nil
	}
	a.say("%s", capitalise(what))
	if res := a.run(ctx, argv, stdin); res.Failed() {
		return fmt.Errorf("%s: %s", what, failure(res))
	}
	return nil
}

func (a *Applier) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	a.Warnings = append(a.Warnings, msg)
	a.say("Warning: %s", msg)
}

func (a *Applier) say(format string, args ...any) {
	if a.Out != nil {
		fmt.Fprintf(a.Out, format+"\n", args...)
	}
}

// preflight checks that the declared gateway is configured in the CLI and its
// login is valid, before anything is pushed.
func (a *Applier) preflight(ctx context.Context) error {
	res := a.run(ctx, a.openshell("status", "-o", "json"), nil)
	login := fmt.Sprintf("log in with `openshell gateway login %s`", a.State.Gateway)
	if res.Failed() {
		return fmt.Errorf("gateway %s: %s; %s", a.State.Gateway, failure(res), login)
	}
	var st struct {
		Gateway        string `json:"gateway"`
		Status         string `json:"status"`
		Authentication struct {
			Status string `json:"status"`
		} `json:"authentication"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &st); err != nil {
		return fmt.Errorf("openshell status: %w", err)
	}
	if st.Gateway != a.State.Gateway || st.Status != "connected" || st.Authentication.Status != "authenticated" {
		return fmt.Errorf("gateway %s is %s, login %s; %s",
			a.State.Gateway, orUnknown(st.Status), orUnknown(st.Authentication.Status), login)
	}
	return nil
}

// images pushes every image the registry does not have. A tag is the image's
// store hash, so an existing tag always holds the same image.
func (a *Applier) images(ctx context.Context) error {
	seen := map[string]bool{}
	for _, name := range desired.SortedKeys(a.State.Types) {
		img := a.State.Types[name].Image
		if seen[img.Ref] {
			continue
		}
		seen[img.Ref] = true
		res := a.run(ctx, []string{"skopeo", "inspect", "--raw", "docker://" + img.Ref}, nil)
		if !res.Failed() {
			continue
		}
		if !strings.Contains(res.Stderr, "manifest unknown") {
			return fmt.Errorf("skopeo inspect %s: %s", img.Ref, failure(res))
		}
		err := a.change(ctx, "push image "+img.Ref,
			[]string{"skopeo", "copy", "docker-archive:" + img.Archive, "docker://" + img.Ref}, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

// workspace creates the declared workspace if it is missing, and makes the
// worker a member with role user.
func (a *Applier) workspace(ctx context.Context) error {
	ws := a.State.Workspace
	res := a.run(ctx, a.openshell("workspace", "get", ws), nil)
	created := false
	switch {
	case !res.Failed():
	case notFound(res):
		if err := a.change(ctx, "create workspace "+ws, a.openshell("workspace", "create", "--name", ws), nil); err != nil {
			return err
		}
		created = true
	default:
		return fmt.Errorf("workspace get %s: %s", ws, failure(res))
	}

	role := ""
	if !(created && a.DryRun) {
		res = a.run(ctx, a.openshell("workspace", "member", "list", "--workspace", ws, "-o", "json"), nil)
		if res.Failed() {
			return fmt.Errorf("workspace member list: %s", failure(res))
		}
		var members struct {
			Members []struct {
				Subject string `json:"subject"`
				Role    string `json:"role"`
			} `json:"members"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &members); err != nil {
			return fmt.Errorf("workspace member list: %w", err)
		}
		for _, m := range members.Members {
			if m.Subject == a.Subject {
				role = m.Role
			}
		}
	}
	switch role {
	case "user":
		return nil
	case "":
		return a.change(ctx, fmt.Sprintf("add %s to workspace %s as user", a.Subject, ws),
			a.openshell("workspace", "member", "add", "--workspace", ws, "--subject", a.Subject, "--role", "user"), nil)
	default:
		a.warn("%s is a member of workspace %s as %s, not user; left as it is", a.Subject, ws, role)
		return nil
	}
}

// workspacePending reports a dry run whose workspace does not exist yet, so
// nothing inside it can be read.
func (a *Applier) workspacePending() bool {
	if !a.DryRun {
		return false
	}
	for _, act := range a.Actions {
		if act == "create workspace "+a.State.Workspace {
			return true
		}
	}
	return false
}

// profiles imports missing profiles and updates the ones whose content
// differs, comparing `profile export -o json` with the declared spec.
func (a *Applier) profiles(ctx context.Context) error {
	pending := a.workspacePending()
	for _, id := range desired.SortedKeys(a.State.Profiles) {
		p := a.State.Profiles[id]
		var res runner.Result
		if !pending {
			res = a.run(ctx, a.inWorkspace("profile", "export", id, "-o", "json"), nil)
		}
		switch {
		case pending || notFound(res):
			if err := a.change(ctx, "import profile "+id, a.inWorkspace("profile", "import", "-f", p.File), nil); err != nil {
				return err
			}
		case res.Failed():
			return fmt.Errorf("profile export %s: %s", id, failure(res))
		default:
			var have map[string]any
			if err := json.Unmarshal([]byte(res.Stdout), &have); err != nil {
				return fmt.Errorf("profile export %s: %w", id, err)
			}
			if SameProfile(p.Spec, have) {
				continue
			}
			if err := a.updateProfile(ctx, id, p.Spec, have["resource_version"]); err != nil {
				return err
			}
		}
	}
	if pending {
		return nil
	}
	res := a.run(ctx, a.inWorkspace("profile", "list", "-o", "json"), nil)
	if res.Failed() {
		return fmt.Errorf("profile list: %s", failure(res))
	}
	var listed []struct {
		ID     string `json:"id"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &listed); err != nil {
		return fmt.Errorf("profile list: %w", err)
	}
	for _, p := range listed {
		if _, ok := a.State.Profiles[p.ID]; !ok && p.Source == "user" {
			a.warn("profile %s in workspace %s is not declared", p.ID, a.State.Workspace)
		}
	}
	return nil
}

// updateProfile writes the declared spec with the exported resource_version,
// which the gateway checks so a concurrent change is not overwritten. JSON is
// YAML, which `profile update -f` reads.
func (a *Applier) updateProfile(ctx context.Context, id string, spec map[string]any, version any) error {
	what := "update profile " + id
	if a.DryRun {
		return a.change(ctx, what, nil, nil)
	}
	doc := map[string]any{}
	for k, v := range spec {
		doc[k] = v
	}
	doc["resource_version"] = version
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "agents-apply-profile-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return a.change(ctx, what, a.inWorkspace("profile", "update", "-f", f.Name(), id), nil)
}

// serverFields are set by the gateway, never by a profile file.
var serverFields = []string{"resource_version", "source", "scope"}

// SameProfile reports whether an exported profile matches the declared spec:
// equal once the gateway's own fields are dropped, and ignoring fields the
// export fills with an empty value that the spec leaves out.
func SameProfile(spec, exported map[string]any) bool {
	have := map[string]any{}
	for k, v := range exported {
		have[k] = v
	}
	for _, k := range serverFields {
		delete(have, k)
	}
	for k, v := range have {
		if _, declared := spec[k]; !declared && isEmpty(v) {
			delete(have, k)
		}
	}
	return reflect.DeepEqual(normalise(spec), normalise(have))
}

// normalise round-trips a value through JSON so both sides use the same
// types (float64 numbers, []any, map[string]any).
func normalise(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(data, &out) != nil {
		return v
	}
	return out
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// providers creates missing providers. One that exists with another profile
// is reported, not changed.
func (a *Applier) providers(ctx context.Context) error {
	have := map[string]string{}
	if !a.workspacePending() {
		res := a.run(ctx, a.inWorkspace("provider", "list", "-o", "json"), nil)
		if res.Failed() {
			return fmt.Errorf("provider list: %s", failure(res))
		}
		var listed struct {
			Providers []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"providers"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &listed); err != nil {
			return fmt.Errorf("provider list: %w", err)
		}
		for _, p := range listed.Providers {
			have[p.Name] = p.Type
		}
	}
	for _, name := range desired.SortedKeys(a.State.Providers) {
		want := a.State.Providers[name].Profile
		got, ok := have[name]
		switch {
		case !ok:
			err := a.change(ctx, fmt.Sprintf("create provider %s (profile %s)", name, want),
				a.inWorkspace("provider", "create", "--name", name, "--type", want), nil)
			if err != nil {
				return err
			}
		case got != want:
			a.warn("provider %s has profile %s, declared %s; left as it is", name, got, want)
		}
	}
	for _, name := range desired.SortedKeys(have) {
		if _, ok := a.State.Providers[name]; !ok {
			a.warn("provider %s in workspace %s is not declared", name, a.State.Workspace)
		}
	}
	return nil
}

// configMap publishes the desired state for the worker, unless the ConfigMap
// already holds exactly this file.
func (a *Applier) configMap(ctx context.Context) error {
	res := a.run(ctx, []string{"kubectl", "-n", a.Namespace, "get", "configmap", ConfigMap, "-o", "json"}, nil)
	switch {
	case !res.Failed():
		var cm struct {
			Data map[string]string `json:"data"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &cm); err != nil {
			return fmt.Errorf("kubectl get configmap: %w", err)
		}
		if cm.Data[ConfigMapKey] == string(a.State.Raw) {
			return nil
		}
	case !strings.Contains(res.Stderr, "NotFound"):
		return fmt.Errorf("kubectl get configmap %s: %s", ConfigMap, failure(res))
	}
	manifest, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      ConfigMap,
			"namespace": a.Namespace,
			"labels":    map[string]string{"app.kubernetes.io/managed-by": "agents-apply"},
		},
		"data": map[string]string{ConfigMapKey: string(a.State.Raw)},
	})
	if err != nil {
		return err
	}
	return a.change(ctx, fmt.Sprintf("write ConfigMap %s/%s", a.Namespace, ConfigMap),
		[]string{"kubectl", "apply", "-f", "-"}, manifest)
}

func notFound(res runner.Result) bool {
	return res.Failed() && strings.Contains(res.Stderr, "not found")
}

// failure describes a failed command by its error output.
func failure(res runner.Result) string {
	if res.Err != nil {
		return res.Err.Error()
	}
	msg := strings.TrimSpace(res.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(res.Stdout)
	}
	return fmt.Sprintf("exit %d: %s", res.ExitCode, msg)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
