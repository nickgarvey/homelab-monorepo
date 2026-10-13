package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testApp runs the CLI against api (a URL), at now, writing the outbox in a
// temporary directory.
type testApp struct {
	t      *testing.T
	env    map[string]string
	now    time.Time
	outbox string
}

func newTestApp(t *testing.T, api string, now time.Time) *testApp {
	outbox := filepath.Join(t.TempDir(), "outbox", "wakeup.json")
	return &testApp{t: t, now: now, outbox: outbox, env: map[string]string{
		"HABIT_API_URL": api,
		"HABIT_OUTBOX":  outbox,
	}}
}

func (ta *testApp) run(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	a := &app{
		stdout: &out, stderr: &errb,
		getenv: func(k string) string { return ta.env[k] },
		now:    func() time.Time { return ta.now },
	}
	code = a.run(context.Background(), args)
	return code, out.String(), errb.String()
}

// noAPI is a PostgREST that fails the test if anything calls it.
func noAPI(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL)
		http.Error(w, "unexpected", 500)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

var testNow = time.Date(2026, 10, 14, 0, 30, 0, 0, time.UTC) // 2026-10-13 17:30 PDT

// Mistakes on the command line are refused before any request, with exit 2,
// what is wrong, and the command's usage.
func TestUsageErrors(t *testing.T) {
	ta := newTestApp(t, noAPI(t), testNow)
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "usage: habit <command>"},
		{[]string{"frobnicate"}, `unknown command "frobnicate"`},
		{[]string{"delete", "x"}, `unknown command "delete"`},
		{[]string{"add"}, "missing arguments"},
		{[]string{"add", "a", "b"}, "too many arguments"},
		{[]string{"add", " padded"}, "no leading or trailing spaces"},
		{[]string{"add", strings.Repeat("n", 101)}, "at most 100 characters"},
		{[]string{"add", "meditate", "--cue", "every morning"}, `"if <event>, then <behavior>"`},
		{[]string{"add", "meditate", "--motivation", " "}, "--motivation is empty"},
		{[]string{"add", "meditate", "--bogus", "x"}, "flag provided but not defined: -bogus"},
		{[]string{"edit", "meditate"}, "nothing to change"},
		{[]string{"edit", "meditate", "--name", ""}, "--name"},
		{[]string{"log", "meditate"}, "missing arguments"},
		{[]string{"log", "meditate", "yes"}, `date "yes"`},
		{[]string{"log", "meditate", "today"}, "want done or missed"},
		{[]string{"log", "meditate", "today", "skipped"}, `got "skipped"`},
		{[]string{"log", "meditate", "13/10", "done"}, `date "13/10"`},
		{[]string{"pending", "someday"}, `date "someday"`},
		{[]string{"pending", "a", "b"}, "too many arguments"},
		{[]string{"history", "meditate", "--days", "0"}, "--days 0"},
		{[]string{"history", "meditate", "--days", "400"}, "--days 400"},
		{[]string{"show", "meditate", "--days", "-1"}, "--days -1"},
		{[]string{"note", "meditate", "mood", "happy"}, `kind "mood"`},
		{[]string{"note", "meditate", "cue"}, "missing arguments"},
		{[]string{"note", "meditate", "cue", " "}, "note text is empty"},
		{[]string{"cadence", "--every", "3", "--reason", "x"}, "name the habit"},
		{[]string{"cadence", "meditate", "--every", "3"}, "needs --reason"},
		{[]string{"cadence", "meditate", "--every", "61", "--reason", "x"}, "--every 61"},
		{[]string{"cadence", "meditate", "--every", "0", "--reason", "x"}, "--every 0"},
		{[]string{"cadence", "meditate", "--next", "2026-10-20"}, "needs --reason"},
		{[]string{"cadence", "meditate", "--auto", "--every", "3", "--reason", "x"}, "cannot be combined"},
		{[]string{"cadence", "meditate", "--auto", "--reason", "x"}, "--auto takes no --reason"},
		{[]string{"cadence", "--reason", "x"}, "--reason goes with"},
		{[]string{"cadence", "--at", "8pm"}, "--at"},
		{[]string{"wake-at", "+1d"}, "missing arguments"},
		{[]string{"wake-at", "soon", "note"}, `time "soon"`},
		{[]string{"wake-at", "+1d", "note", "--at", "25:00"}, "--at"},
		{[]string{"list", "--tz", "Mars/Olympus"}, `time zone "Mars/Olympus"`},
		{[]string{"list", "extra"}, "too many arguments"},
	} {
		code, out, errOut := ta.run(c.args...)
		if code != 2 {
			t.Errorf("%q: exit %d; want 2 (stdout %q, stderr %q)", c.args, code, out, errOut)
			continue
		}
		if !strings.Contains(errOut, c.want) {
			t.Errorf("%q: stderr %q; want it to contain %q", c.args, errOut, c.want)
		}
		if len(c.args) > 0 && findCommand(c.args[0]) != nil && !strings.Contains(errOut, "usage: habit "+c.args[0]) {
			t.Errorf("%q: stderr %q has no usage line", c.args, errOut)
		}
	}
}

// Refusals that are not command-line mistakes: exit 1, no request.
func TestRefusedBeforeRequest(t *testing.T) {
	ta := newTestApp(t, noAPI(t), testNow)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"log", "meditate", "2026-10-14", "done"}, "2026-10-14 is after today (2026-10-13"},
		{[]string{"log", "meditate", "--tz", "UTC", "2026-10-15", "done"}, "after today (2026-10-14"},
		{[]string{"wake-at", "+5m", "check"}, "at least 10m ahead"},
		{[]string{"wake-at", "+61d", "check"}, "at most 60d ahead"},
		{[]string{"wake-at", "+1h", strings.Repeat("x", 501)}, "at most 500"},
		{[]string{"wake-at", "+1h", ""}, "the note is empty"},
	} {
		code, _, errOut := ta.run(c.args...)
		if code != 1 || !strings.Contains(errOut, c.want) {
			t.Errorf("%q: exit %d, stderr %q; want 1 and %q", c.args, code, errOut, c.want)
		}
	}
	if _, err := os.Stat(ta.outbox); err == nil {
		t.Error("a refused wake-up left an outbox file")
	}
}

func TestHelp(t *testing.T) {
	ta := newTestApp(t, noAPI(t), testNow)
	for _, args := range [][]string{{"--help"}, {"help"}, {"-h"}} {
		code, out, _ := ta.run(args...)
		if code != 0 || !strings.Contains(out, "Onboarding:") {
			t.Errorf("%q: exit %d, %q", args, code, out)
		}
	}
	for _, c := range commands {
		for _, args := range [][]string{{c.name, "--help"}, {"help", c.name}, {c.name, "x", "-h"}} {
			code, out, _ := ta.run(args...)
			if code != 0 || !strings.Contains(out, "usage: habit "+c.name) {
				t.Errorf("%q: exit %d, %q", args, code, out)
			}
		}
		if !strings.Contains(topHelp, c.name) {
			t.Errorf("top help does not mention %s", c.name)
		}
	}
	_, out, _ := ta.run("cadence", "--help")
	if !strings.Contains(out, cadenceRule) {
		t.Error("habit cadence --help lacks the rule")
	}
}

func TestParseInterleaved(t *testing.T) {
	ta := newTestApp(t, noAPI(t), testNow)
	// Flags after the arguments, and "--" before an argument that starts
	// with a dash.
	ta.env["HABIT_TZ"] = "UTC"
	code, out, errOut := ta.run("wake-at", "--json", "+2h", "--", "-- dashes in the note")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got["at"] != "2026-10-14T02:30:00Z" || got["note"] != "-- dashes in the note" {
		t.Errorf("got %v", got)
	}
}

func TestWakeAtWritesOutbox(t *testing.T) {
	ta := newTestApp(t, noAPI(t), testNow)
	code, out, errOut := ta.run("wake-at", "tomorrow", "end-of-day", "check-in", "--at", "21:30")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "2026-10-14T21:30:00-07:00 (Wed, in 1d 4h)") {
		t.Errorf("stdout %q", out)
	}
	data, err := os.ReadFile(ta.outbox)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"at":"2026-10-14T21:30:00-07:00","note":"end-of-day check-in"}` + "\n"; string(data) != want {
		t.Errorf("outbox %s; want %s", data, want)
	}
}

// Errors from PostgREST and the proxy say what to do.
func TestAPIErrors(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"code":"PGRST301","message":"No suitable key or wrong key type"}`, "the sandbox needs the habit-data provider and the habit-helper agent type"},
		{502, `upstream token grant failed`, "got no token"},
		{403, `{"error":"policy_denied"}`, "the sandbox proxy refused the request (HTTP 403"},
		{400, `{"code":"22P02","message":"invalid input syntax","details":null,"hint":null}`, "PostgREST: HTTP 400: invalid input syntax"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				t.Errorf("the CLI sent Authorization")
			}
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		}))
		ta := newTestApp(t, srv.URL, testNow)
		code, _, errOut := ta.run("list")
		srv.Close()
		if code != 1 || !strings.Contains(errOut, c.want) {
			t.Errorf("HTTP %d: exit %d, %q; want %q", c.status, code, errOut, c.want)
		}
	}
}

// Proxy variables are ignored: the sandbox's egress is captured
// transparently, so a stray HTTP_PROXY must not divert requests.
func TestIgnoresProxyEnv(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()
	// httptest listens on 127.0.0.1, which ProxyFromEnvironment would skip
	// anyway, so check the transport itself.
	c := newClient(srv.URL)
	if c.http.Transport.(*http.Transport).Proxy != nil {
		t.Error("the transport uses a proxy function")
	}
	ta := newTestApp(t, srv.URL, testNow)
	if code, out, errOut := ta.run("list"); code != 0 || !strings.Contains(out, "no habits yet") {
		t.Errorf("exit %d %q %q", code, out, errOut)
	}
}
