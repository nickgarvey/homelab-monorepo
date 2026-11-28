package bot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ChangedInputs lists the flake.lock nodes whose locked source differs
// between before and after, or that only one of them has, sorted. Node
// names are what `nix flake update` reports (nixpkgs, home-manager, and the
// _2 suffixes of transitive inputs).
func ChangedInputs(before, after []byte) ([]string, error) {
	b, err := lockNodes(before)
	if err != nil {
		return nil, fmt.Errorf("old flake.lock: %w", err)
	}
	a, err := lockNodes(after)
	if err != nil {
		return nil, fmt.Errorf("new flake.lock: %w", err)
	}
	var changed []string
	for name, locked := range a {
		if old, ok := b[name]; !ok || !bytes.Equal(old, locked) {
			changed = append(changed, name)
		}
	}
	for name := range b {
		if _, ok := a[name]; !ok {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed, nil
}

func lockNodes(data []byte) (map[string]json.RawMessage, error) {
	var lock struct {
		Root  string `json:"root"`
		Nodes map[string]struct {
			Locked json.RawMessage `json:"locked"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	for name, n := range lock.Nodes {
		if name == lock.Root || n.Locked == nil {
			continue
		}
		var v any
		if err := json.Unmarshal(n.Locked, &v); err != nil {
			return nil, err
		}
		canon, _ := json.Marshal(v) // map keys sorted: formatting-independent
		out[name] = canon
	}
	return out, nil
}

// LockTrailer marks the bot's own `nix flake update` commit. It is not
// carried forward: each run makes a fresh one.
const LockTrailer = "Flake-Bot: lock-update"

// LockCommitMessage is the bot's commit for a flake.lock update, in the
// repo's commit shape (AGENTS.md, "Commit messages").
func LockCommitMessage(inputs []string) string {
	subject := "Flake update"
	if len(inputs) > 0 {
		subject += ": " + strings.Join(inputs, ", ")
	}
	if len(subject) > 200 {
		subject = fmt.Sprintf("Flake update: %d inputs", len(inputs))
	}
	return subject + "\n\nHuman Text:\nNone\n\nLLM Text:\n\n" + LockTrailer + "\n"
}
