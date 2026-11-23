package watch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]Version{
		"4.39.28":      {4, 39, 28, ""},
		"v4.29.3":      {4, 29, 3, ""},
		">4.28.0":      {4, 28, 0, ""},
		" 4.39.0":      {4, 39, 0, ""},
		"4.40.0-beta1": {4, 40, 0, "beta1"},
		"2":            {2, 0, 0, ""},
	} {
		got, err := ParseVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "latest", "4.x", "1.2.3.4"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) parsed", in)
		}
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"4.39.28", "4.39.28", 0},
		{"4.39.9", "4.39.28", -1},
		{"4.40.0", "4.39.28", 1},
		{"5.0.0", "4.99.99", 1},
		{"4.40.0-beta1", "4.40.0", -1},
		{"4.40.0-beta2", "4.40.0-beta1", 1},
	} {
		if got := mustVersion(t, c.a).Compare(mustVersion(t, c.b)); got != c.want {
			t.Errorf("%s vs %s = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestImageTag(t *testing.T) {
	for in, want := range map[string]string{
		"ghcr.io/authelia/authelia:4.39.28":            "4.39.28",
		"localhost:5000/x:1.2":                         "1.2",
		"ghcr.io/authelia/authelia:4.39.28@sha256:abc": "4.39.28",
	} {
		if got, err := ImageTag(in); err != nil || got != want {
			t.Errorf("ImageTag(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"ghcr.io/authelia/authelia", "localhost:5000/x", "x@sha256:abc"} {
		if _, err := ImageTag(in); err == nil {
			t.Errorf("ImageTag(%q) has a tag", in)
		}
	}
}

// Authelia's advisories as GitHub listed them on 2026-11-22.
func autheliaAdvisories() []Advisory {
	adv := func(id, sev, patched string) Advisory {
		return Advisory{GHSAID: id, Severity: sev, State: "published", Vulnerabilities: []Vulnerability{{PatchedVersions: patched}}}
	}
	return []Advisory{
		adv("GHSA-j748-h363-wqj8", "low", "4.39.20"),
		adv("GHSA-hjj4-hfjm-fmrj", "medium", "4.39.20"),
		adv("GHSA-gmfg-3v4q-9qr4", "low", "4.39.16"),
		adv("GHSA-m5mf-3963-4x26", "low", "4.38.19,4.39.0"),
		adv("GHSA-x883-2vmg-xwf7", "low", "4.38.0"),
		adv("GHSA-68wm-pfjf-wqp6", "high", "v4.29.3"),
		adv("GHSA-36f2-fcrx-fp4j", "medium", ">4.28.0"),
	}
}

func fixIDs(fs []Fix) []string {
	var ids []string
	for _, f := range fs {
		ids = append(ids, f.ID+"@"+f.Patched)
	}
	return ids
}

func TestFixes(t *testing.T) {
	for _, c := range []struct {
		running, latest string
		want            []string
	}{
		{"4.39.28", "4.39.28", nil},
		{"4.39.19", "4.39.28", []string{"GHSA-j748-h363-wqj8@4.39.20", "GHSA-hjj4-hfjm-fmrj@4.39.20"}},
		// Not yet released: only what latest fixes counts.
		{"4.39.15", "4.39.18", []string{"GHSA-gmfg-3v4q-9qr4@4.39.16"}},
		// A backport on its own line counts as patched.
		{"4.38.20", "4.39.28", []string{"GHSA-j748-h363-wqj8@4.39.20", "GHSA-hjj4-hfjm-fmrj@4.39.20", "GHSA-gmfg-3v4q-9qr4@4.39.16"}},
		{"4.38.10", "4.39.28", []string{"GHSA-j748-h363-wqj8@4.39.20", "GHSA-hjj4-hfjm-fmrj@4.39.20", "GHSA-gmfg-3v4q-9qr4@4.39.16", "GHSA-m5mf-3963-4x26@4.38.19"}},
	} {
		got := fixIDs(Fixes(mustVersion(t, c.running), mustVersion(t, c.latest), autheliaAdvisories()))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Fixes(%s, %s) = %v, want %v", c.running, c.latest, got, c.want)
		}
	}
	withdrawn := "2026-01-01T00:00:00Z"
	advs := []Advisory{
		{GHSAID: "w", State: "published", WithdrawnAt: &withdrawn, Vulnerabilities: []Vulnerability{{PatchedVersions: "4.39.20"}}},
		{GHSAID: "d", State: "draft", Vulnerabilities: []Vulnerability{{PatchedVersions: "4.39.20"}}},
		{GHSAID: "u", State: "published", Vulnerabilities: []Vulnerability{{PatchedVersions: "soon"}}},
	}
	if got := Fixes(mustVersion(t, "4.39.0"), mustVersion(t, "4.39.28"), advs); got != nil {
		t.Errorf("withdrawn, draft or unparseable advisories counted: %v", got)
	}
}

type fakeKube struct {
	images []string
	err    error
}

func (f fakeKube) RunningImages(context.Context, string, string, string) ([]string, error) {
	return f.images, f.err
}

func fakeGitHub(t *testing.T, tag string) *GitHub {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "no user agent", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/repos/authelia/authelia/releases/latest":
			w.Write([]byte(`{"tag_name":"` + tag + `","html_url":"https://example/rel"}`))
		case "/repos/authelia/authelia/security-advisories":
			w.Write([]byte(`[{"ghsa_id":"GHSA-1","severity":"high","state":"published","withdrawn_at":null,
				"html_url":"https://example/adv","vulnerabilities":[{"patched_versions":"4.39.20"}]}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &GitHub{Client: srv.Client(), BaseURL: srv.URL}
}

func TestCheck(t *testing.T) {
	ctx := context.Background()
	target := Targets[0]

	a := &Activities{GitHub: fakeGitHub(t, "v4.39.28"), Kube: fakeKube{images: []string{
		"ghcr.io/authelia/authelia:4.39.28", "ghcr.io/authelia/authelia:4.39.19",
	}}}
	r, err := a.Check(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	want := TargetResult{Name: "authelia", Repo: "authelia/authelia", Running: "4.39.19", Latest: "v4.39.28",
		LatestURL: "https://example/rel", Behind: true,
		Fixes: []Fix{{ID: "GHSA-1", Severity: "high", Patched: "4.39.20", URL: "https://example/adv"}}}
	if !reflect.DeepEqual(r, want) {
		t.Errorf("Check = %+v, want %+v", r, want)
	}

	a.Kube = fakeKube{images: []string{"ghcr.io/authelia/authelia:4.39.28"}}
	if r, err := a.Check(ctx, target); err != nil || r.Behind || len(r.Fixes) != 0 {
		t.Errorf("up to date: %+v, %v", r, err)
	}

	for _, k := range []fakeKube{{}, {err: errors.New("forbidden")}, {images: []string{"ghcr.io/authelia/authelia:latest"}}} {
		if _, err := (&Activities{GitHub: a.GitHub, Kube: k}).Check(ctx, target); err == nil {
			t.Errorf("Check with %+v succeeded", k)
		}
	}
	a.GitHub = &GitHub{Client: http.DefaultClient, BaseURL: "http://127.0.0.1:1"}
	if _, err := a.Check(ctx, target); err == nil {
		t.Error("Check without GitHub succeeded")
	}
}

func TestRunningImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/api/v1/namespaces/authelia/pods" ||
			r.URL.Query().Get("labelSelector") != "app=authelia" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		w.Write([]byte(`{"items":[
			{"metadata":{"name":"a"},"spec":{"containers":[{"name":"caddy","image":"caddy:2"},{"name":"authelia","image":"ghcr.io/authelia/authelia:4.39.28"}]},"status":{"phase":"Running"}},
			{"metadata":{"name":"b"},"spec":{"containers":[{"name":"authelia","image":"ghcr.io/authelia/authelia:4.39.29"}]},"status":{"phase":"Pending"}}]}`))
	}))
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	k := &Kube{Client: srv.Client(), BaseURL: srv.URL, TokenFile: tok}
	got, err := k.RunningImages(context.Background(), "authelia", "app=authelia", "authelia")
	if err != nil || !reflect.DeepEqual(got, []string{"ghcr.io/authelia/authelia:4.39.28"}) {
		t.Errorf("RunningImages = %v, %v", got, err)
	}
	if _, err := k.RunningImages(context.Background(), "other", "app=authelia", "authelia"); err == nil ||
		!strings.Contains(err.Error(), "403") {
		t.Errorf("refused list: %v", err)
	}
}

func TestWorkflow(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})
	r := TargetResult{Name: "authelia", Running: "4.39.28", Latest: "v4.39.28"}
	env.OnActivity("Check", mock.Anything, Targets[0]).Return(r, nil)
	env.ExecuteWorkflow(ReleaseWatch)
	var res Result
	if err := env.GetWorkflowResult(&res); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res, Result{Targets: []TargetResult{r}}) {
		t.Errorf("result %+v", res)
	}

	env = s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})
	env.OnActivity("Check", mock.Anything, mock.Anything).Return(TargetResult{}, errors.New("rate limited"))
	env.ExecuteWorkflow(ReleaseWatch)
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("failed check: %v", err)
	}
}
