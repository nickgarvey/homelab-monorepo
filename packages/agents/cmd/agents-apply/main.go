// agents-apply publishes the agent types declared in agents/declaration.nix:
// it pushes their images to the registry, creates the gateway workspace and
// the worker's membership of it, imports or updates their provider profiles,
// creates their providers, and last writes the desired state to the
// agents-desired-state ConfigMap that the worker reads. It deletes nothing.
//
// It runs the openshell, skopeo and kubectl CLIs with the operator's own
// gateway login and cluster access. The flake's agents-apply wraps it with
// --state set to the desired state built from the declaration.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"homelab/agents/internal/apply"
	"homelab/agents/internal/desired"
	"homelab/agents/internal/runner"
)

func main() {
	state := flag.String("state", "", "Desired-state JSON (nix build .#agents-desired-state)")
	dryRun := flag.Bool("dry-run", false, "Read the gateway and cluster, print what would change, change nothing")
	namespace := flag.String("namespace", "agents", "Namespace of the worker and its ConfigMap")
	subject := flag.String("subject", "agents-worker", "The worker's gateway user, made a member of the workspace")
	flag.Parse()
	if *state == "" {
		fmt.Fprintln(os.Stderr, "agents-apply: --state is required")
		os.Exit(2)
	}

	s, err := desired.Load(*state)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agents-apply:", err)
		os.Exit(1)
	}
	a := &apply.Applier{
		Runner:    runner.Exec{},
		State:     s,
		Namespace: *namespace,
		Subject:   *subject,
		DryRun:    *dryRun,
		Out:       os.Stdout,
	}
	if err := a.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "agents-apply:", err)
		os.Exit(1)
	}
}
