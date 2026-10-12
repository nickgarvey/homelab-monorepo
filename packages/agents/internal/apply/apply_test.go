package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"homelab/agents/internal/desired"
	"homelab/agents/internal/runner"
)

const stateJSON = `{
  "gateway": "k8s",
  "workspace": "agents",
  "profiles": {
    "litellm": {
      "file": "/nix/store/x-litellm-profile.yaml",
      "spec": {"id": "litellm", "category": "inference", "endpoints": [{"host": "litellm", "port": 4000}]}
    }
  },
  "providers": {"litellm": {"profile": "litellm"}},
  "types": {
    "pi": {
      "image": {"ref": "oci.garvey.sh/pi-agent:abc", "archive": "/nix/store/y-pi-agent.tar.gz", "cmd": ["/bin/sleep", "infinity"]},
      "policy": {"file": "/nix/store/z-policy.yaml", "spec": {"version": 1}},
      "providers": ["litellm"],
      "session": {"idleTimeoutSeconds": 21600, "retentionSeconds": 604800, "primeMessages": 50},
      "version": "v1"
    }
  }
}`

// world is a fake gateway, registry and cluster that keeps state between
// calls, so a second run sees what the first one did.
type world struct {
	loggedIn   bool
	inspectErr string // a skopeo inspect failure other than a missing tag
	images     map[string]bool
	workspace  bool
	members    map[string]string
	profiles   map[string]map[string]any
	providers  map[string]string
	configMap  *string
	changes    []string
}

func newWorld() *world {
	return &world{
		loggedIn:  true,
		images:    map[string]bool{},
		members:   map[string]string{},
		profiles:  map[string]map[string]any{},
		providers: map[string]string{},
	}
}

func ok(stdout string) runner.Result { return runner.Result{Stdout: stdout} }

func fail(stderr string) runner.Result { return runner.Result{ExitCode: 1, Stderr: stderr} }

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// flagValue returns the argument after flag in argv.
func flagValue(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func (w *world) Run(_ context.Context, argv []string, opts runner.Opts) runner.Result {
	joined := strings.Join(argv, " ")
	change := func(r runner.Result) runner.Result {
		w.changes = append(w.changes, joined)
		return r
	}
	switch argv[0] {
	case "skopeo":
		ref := strings.TrimPrefix(argv[len(argv)-1], "docker://")
		if argv[1] == "inspect" {
			if w.inspectErr != "" {
				return fail(w.inspectErr)
			}
			if w.images[ref] {
				return ok("{}")
			}
			repo, _, _ := strings.Cut(ref, ":")
			for r := range w.images {
				if strings.HasPrefix(r, repo+":") {
					return fail(`level=fatal msg="reading manifest abc in oci.garvey.sh/pi-agent: manifest unknown"`)
				}
			}
			return fail(`level=fatal msg="Error parsing image name \"docker://` + ref + `\": reading manifest abc in oci.garvey.sh/pi-agent: repository name not known to registry"`)
		}
		w.images[ref] = true
		return change(ok(""))
	case "kubectl":
		if argv[3] == "get" {
			if w.configMap == nil {
				return fail(`Error from server (NotFound): configmaps "agents-desired-state" not found`)
			}
			return ok(mustJSON(map[string]any{"data": map[string]string{ConfigMapKey: *w.configMap}}))
		}
		var cm struct {
			Data map[string]string `json:"data"`
		}
		if err := json.Unmarshal(opts.Stdin, &cm); err != nil {
			return fail(err.Error())
		}
		s := cm.Data[ConfigMapKey]
		w.configMap = &s
		return change(ok(""))
	}

	// openshell -g k8s [--workspace agents] <command...>
	args := argv[3:]
	if args[0] == "--workspace" {
		args = args[2:]
	}
	cmd := strings.Join(args[:2], " ")
	switch cmd {
	case "status -o":
		auth := "authenticated"
		if !w.loggedIn {
			auth = "unauthenticated"
		}
		return ok(mustJSON(map[string]any{"gateway": "k8s", "status": "connected", "authentication": map[string]string{"status": auth}}))
	case "workspace get":
		if !w.workspace {
			return fail(`code: 'Some requested entity was not found', message: "workspace not found"`)
		}
		return ok("Workspace: agents")
	case "workspace create":
		w.workspace = true
		return change(ok(""))
	case "workspace member":
		if args[2] == "list" {
			var ms []map[string]string
			for s, r := range w.members {
				ms = append(ms, map[string]string{"subject": s, "role": r})
			}
			return ok(mustJSON(map[string]any{"members": ms}))
		}
		w.members[flagValue(args, "--subject")] = flagValue(args, "--role")
		return change(ok(""))
	case "profile export":
		p, found := w.profiles[args[2]]
		if !found {
			return fail(`code: 'Some requested entity was not found', message: "provider profile not found"`)
		}
		return ok(mustJSON(p))
	case "profile import", "profile update":
		data, err := os.ReadFile(flagValue(args, "-f"))
		if args[1] == "import" {
			// The import reads the store file, which the test does not
			// have; it stands for the declared spec.
			data, err = []byte(stateSpec(w)), nil
		}
		if err != nil {
			return fail(err.Error())
		}
		var p map[string]any
		if err := json.Unmarshal(data, &p); err != nil {
			return fail(err.Error())
		}
		id, _ := p["id"].(string)
		version := 0.0
		if old, found := w.profiles[id]; found {
			version = old["resource_version"].(float64)
			if p["resource_version"] != version {
				return fail("resource_version mismatch")
			}
		}
		p["resource_version"] = version + 1
		p["source"], p["scope"], p["credentials"] = "user", "workspace", []any{}
		w.profiles[id] = p
		return change(ok(""))
	case "profile list":
		var ps []map[string]any
		for _, p := range w.profiles {
			ps = append(ps, p)
		}
		return ok(mustJSON(ps))
	case "provider list":
		var ps []map[string]string
		for n, t := range w.providers {
			ps = append(ps, map[string]string{"name": n, "type": t})
		}
		return ok(mustJSON(map[string]any{"providers": ps}))
	case "provider create":
		w.providers[flagValue(args, "--name")] = flagValue(args, "--type")
		return change(ok(""))
	}
	return fail("unexpected command: " + joined)
}

// stateSpec is the declared litellm spec, which `profile import` of its file
// would create.
func stateSpec(*world) string {
	s, err := desired.Parse([]byte(stateJSON))
	if err != nil {
		panic(err)
	}
	return mustJSON(s.Profiles["litellm"].Spec)
}

func applier(t *testing.T, w *world, dryRun bool) *Applier {
	t.Helper()
	s, err := desired.Parse([]byte(stateJSON))
	if err != nil {
		t.Fatal(err)
	}
	return &Applier{Runner: w, State: s, Namespace: "agents", Subject: "agents-worker", DryRun: dryRun}
}

func TestFirstRunDoesEverythingInOrderAndSecondDoesNothing(t *testing.T) {
	w := newWorld()
	a := applier(t, w, false)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"push image oci.garvey.sh/pi-agent:abc",
		"create workspace agents",
		"add agents-worker to workspace agents as user",
		"import profile litellm",
		"create provider litellm (profile litellm)",
		"write ConfigMap agents/agents-desired-state",
	}
	if fmt.Sprint(a.Actions) != fmt.Sprint(want) {
		t.Fatalf("actions:\n got %q\nwant %q", a.Actions, want)
	}
	if !strings.Contains(w.changes[0], "skopeo copy docker-archive:/nix/store/y-pi-agent.tar.gz docker://oci.garvey.sh/pi-agent:abc") {
		t.Errorf("push: %s", w.changes[0])
	}
	if !strings.Contains(w.changes[3], "--workspace agents profile import -f /nix/store/x-litellm-profile.yaml") {
		t.Errorf("import is not in the workspace: %s", w.changes[3])
	}
	if !strings.Contains(w.changes[4], "--workspace agents provider create --name litellm --type litellm") {
		t.Errorf("provider: %s", w.changes[4])
	}
	if *w.configMap != stateJSON {
		t.Errorf("ConfigMap does not hold the desired state verbatim")
	}

	second := applier(t, w, false)
	if err := second.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(second.Actions) != 0 || len(w.changes) != len(want) {
		t.Errorf("second run changed something: %q", second.Actions)
	}
	if len(second.Warnings) != 0 {
		t.Errorf("second run warned: %q", second.Warnings)
	}
}

func TestChangedProfileIsUpdatedWithItsResourceVersion(t *testing.T) {
	w := newWorld()
	if err := applier(t, w, false).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.profiles["litellm"]["category"] = "other"
	w.changes = nil

	a := applier(t, w, false)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(a.Actions) != "[update profile litellm]" {
		t.Fatalf("actions: %q", a.Actions)
	}
	if !strings.Contains(w.changes[0], "--workspace agents profile update -f ") || !strings.HasSuffix(w.changes[0], " litellm") {
		t.Errorf("update: %s", w.changes[0])
	}
	if got := w.profiles["litellm"]; got["category"] != "inference" || got["resource_version"] != 2.0 {
		t.Errorf("profile after update: %v", got)
	}
}

// The first run pushes to a repository the registry has never held; this is
// a new tag in one it has.
func TestNewTagInKnownRepositoryIsPushed(t *testing.T) {
	w := newWorld()
	w.images["oci.garvey.sh/pi-agent:old"] = true
	a := applier(t, w, false)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(a.Actions) == 0 || a.Actions[0] != "push image oci.garvey.sh/pi-agent:abc" {
		t.Errorf("actions: %q", a.Actions)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	w := newWorld()
	a := applier(t, w, true)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.changes) != 0 {
		t.Errorf("dry run changed: %q", w.changes)
	}
	if len(a.Actions) != 6 {
		t.Errorf("dry run should report 6 actions, got %q", a.Actions)
	}
}

func TestNotLoggedInStopsBeforeAnything(t *testing.T) {
	w := newWorld()
	w.loggedIn = false
	err := applier(t, w, false).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "openshell gateway login k8s") {
		t.Fatalf("err = %v", err)
	}
	if len(w.changes) != 0 {
		t.Errorf("changed: %q", w.changes)
	}
}

func TestRegistryErrorStopsBeforeConfigMap(t *testing.T) {
	w := newWorld()
	w.inspectErr = "unauthorized"
	err := applier(t, w, false).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("err = %v", err)
	}
	if w.configMap != nil || len(w.changes) != 0 {
		t.Errorf("changed after a failed step: %q", w.changes)
	}
}

func TestUndeclaredAndConflictingAreReportedNotChanged(t *testing.T) {
	w := newWorld()
	if err := applier(t, w, false).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.profiles["extra"] = map[string]any{"id": "extra", "source": "user", "resource_version": 1.0}
	w.providers["extra"] = "extra"
	w.providers["litellm"] = "extra"
	w.members["agents-worker"] = "admin"
	w.changes = nil

	a := applier(t, w, false)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.changes) != 0 {
		t.Errorf("changed: %q", w.changes)
	}
	got := strings.Join(a.Warnings, "\n")
	for _, want := range []string{
		"agents-worker is a member of workspace agents as admin",
		"profile extra in workspace agents is not declared",
		"provider litellm has profile extra, declared litellm",
		"provider extra in workspace agents is not declared",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing warning %q in:\n%s", want, got)
		}
	}
}

func TestSameProfile(t *testing.T) {
	spec := map[string]any{"id": "p", "port": 4000, "rules": []any{map[string]any{"a": "b"}}}
	exported := map[string]any{
		"id": "p", "port": 4000.0, "rules": []any{map[string]any{"a": "b"}},
		"resource_version": 3.0, "source": "user", "scope": "workspace",
		"credentials": []any{}, "description": "",
	}
	if !SameProfile(spec, exported) {
		t.Error("equal profiles differ")
	}
	exported["port"] = 4001.0
	if SameProfile(spec, exported) {
		t.Error("a changed port is not seen")
	}
	exported["port"] = 4000.0
	exported["credentials"] = []any{"KEY"}
	if SameProfile(spec, exported) {
		t.Error("a non-empty field the spec lacks is not seen")
	}
}

func TestParseRejectsDanglingReferences(t *testing.T) {
	bad := strings.Replace(stateJSON, `"providers": ["litellm"]`, `"providers": ["missing"]`, 1)
	if _, err := desired.Parse([]byte(bad)); err == nil {
		t.Error("a type with an undeclared provider parses")
	}
}
