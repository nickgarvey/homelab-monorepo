// habit-admin is the habit-admin agent's way to Habit Helper's data and
// wake-ups (agents/habit-admin): it sends its arguments, joined by spaces, to
// the Matrix bridge's admin API (internal/habitadmin, API) and prints the
// reply. The commands are the admin chat's; `habit-admin help` lists them.
//
// The sandbox's egress goes through the OpenShell supervisor, which captures
// connections transparently and puts the token from agent-token in the
// Authorization header. So the CLI dials the URL directly, ignores proxy
// variables, and sends no token of its own.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultURL = "http://agents-matrix.agents.svc.cluster.local:8010"

const usage = `usage: habit-admin <command> [args...]

Runs one admin command on Habit Helper's habits and wake-ups and prints the
reply. habit-admin help lists the commands. Arguments are joined by spaces,
so quoting only matters to the shell.

Environment: HABIT_ADMIN_API_URL (default ` + defaultURL + `).
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, usage)
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}
	reply, err := run(apiURL(), strings.Join(args, " "))
	if err != nil {
		fmt.Fprintln(os.Stderr, "habit-admin:", err)
		os.Exit(1)
	}
	fmt.Println(reply)
}

func apiURL() string {
	if u := os.Getenv("HABIT_ADMIN_API_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return defaultURL
}

func run(base, command string) (string, error) {
	body, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		return "", err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	hc := &http.Client{Transport: tr, Timeout: 90 * time.Second}
	resp, err := hc.Post(base+"/run", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("the admin API: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("the admin API: %w", err)
	}
	var out struct{ Reply, Error string }
	_ = json.Unmarshal(data, &out)
	switch {
	case resp.StatusCode == http.StatusOK:
		return out.Reply, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return "", fmt.Errorf("the admin API refused the request as unauthenticated (%s). "+
			"The token comes from the sandbox supervisor: the sandbox needs the habit-admin-api "+
			"provider and the habit-admin agent type", msg(out.Error, data))
	case resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable ||
		resp.StatusCode == http.StatusGatewayTimeout:
		return "", fmt.Errorf("the sandbox proxy could not complete the request (HTTP %d: %s); "+
			"it got no token, or the bridge is down. Try again in a minute", resp.StatusCode, msg(out.Error, data))
	}
	return "", fmt.Errorf("the admin API: HTTP %d: %s", resp.StatusCode, msg(out.Error, data))
}

func msg(e string, body []byte) string {
	if e != "" {
		return e
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
