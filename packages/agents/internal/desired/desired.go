// Package desired reads the desired-state JSON that agents/desired-state.nix
// builds from agents/declaration.nix. That file documents the format.
package desired

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

type State struct {
	Gateway   string              `json:"gateway"`
	Workspace string              `json:"workspace"`
	Profiles  map[string]Profile  `json:"profiles"`
	Providers map[string]Provider `json:"providers"`
	Types     map[string]Type     `json:"types"`

	// Raw is the file as read, which agents-apply publishes unchanged.
	Raw []byte `json:"-"`
}

type Profile struct {
	// File is the profile YAML in the Nix store, for `profile import -f`.
	File string `json:"file"`
	// Spec is the same profile as JSON.
	Spec map[string]any `json:"spec"`
}

type Provider struct {
	Profile string `json:"profile"`
}

type Type struct {
	Image     Image             `json:"image"`
	Policy    Policy            `json:"policy"`
	Providers []string          `json:"providers"`
	Env       map[string]string `json:"env"`
	Resources Resources         `json:"resources"`
	Session   Session           `json:"session"`
	Version   string            `json:"version"`
}

type Image struct {
	// Ref is where the image is pushed: <registry>/<name>:<store hash>.
	Ref string `json:"ref"`
	// Archive is the image tarball in the Nix store.
	Archive string   `json:"archive"`
	Cmd     []string `json:"cmd"`
}

type Policy struct {
	File string         `json:"file"`
	Spec map[string]any `json:"spec"`
}

type Resources struct {
	CPU    *string `json:"cpu"`
	Memory *string `json:"memory"`
}

type Session struct {
	IdleTimeoutSeconds int `json:"idleTimeoutSeconds"`
	RetentionSeconds   int `json:"retentionSeconds"`
	PrimeMessages      int `json:"primeMessages"`
}

// Parse decodes a desired state and checks the references the Nix evaluation
// already guarantees, so a hand-made file fails here rather than half-applied.
func Parse(data []byte) (*State, error) {
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("desired state: %w", err)
	}
	s.Raw = data
	if s.Gateway == "" || s.Workspace == "" {
		return nil, fmt.Errorf("desired state: gateway and workspace are required")
	}
	for name, p := range s.Providers {
		if _, ok := s.Profiles[p.Profile]; !ok {
			return nil, fmt.Errorf("desired state: provider %s: profile %q is not declared", name, p.Profile)
		}
	}
	for name, t := range s.Types {
		if t.Image.Ref == "" || t.Image.Archive == "" {
			return nil, fmt.Errorf("desired state: type %s: image ref and archive are required", name)
		}
		for _, p := range t.Providers {
			if _, ok := s.Providers[p]; !ok {
				return nil, fmt.Errorf("desired state: type %s: provider %q is not declared", name, p)
			}
		}
	}
	return &s, nil
}

func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// SortedKeys returns a map's keys in order, so runs are deterministic.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
