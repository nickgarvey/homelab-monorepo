package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
)

// policyFile is the part of the sandbox policy YAML format (the CLI's
// --policy) that the worker can pass through the SDK. Network policies are
// not among it: an agent type's network access comes from its providers.
type policyFile struct {
	Version    uint32 `json:"version"`
	Filesystem *struct {
		IncludeWorkdir bool     `json:"include_workdir"`
		ReadOnly       []string `json:"read_only"`
		ReadWrite      []string `json:"read_write"`
	} `json:"filesystem_policy"`
	Landlock *struct {
		Compatibility string `json:"compatibility"`
	} `json:"landlock"`
	Process *struct {
		RunAsUser  string `json:"run_as_user"`
		RunAsGroup string `json:"run_as_group"`
	} `json:"process"`
}

// Policy converts a policy spec (the policy YAML as JSON) to the SDK's form.
// A key outside policyFile is an error, so nothing in a declared policy is
// silently dropped. A nil spec is no policy: the gateway's default.
func Policy(spec map[string]any) (*openshell.SandboxPolicy, error) {
	if spec == nil {
		return nil, nil
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	var f policyFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("sandbox policy: %w (the worker passes version, filesystem_policy, landlock and process)", err)
	}
	p := &openshell.SandboxPolicy{Version: f.Version}
	if f.Filesystem != nil {
		p.Filesystem = &openshell.FilesystemPolicy{
			IncludeWorkdir: f.Filesystem.IncludeWorkdir,
			ReadOnly:       f.Filesystem.ReadOnly,
			ReadWrite:      f.Filesystem.ReadWrite,
		}
	}
	if f.Landlock != nil {
		p.Landlock = &openshell.LandlockPolicy{Compatibility: f.Landlock.Compatibility}
	}
	if f.Process != nil {
		p.Process = &openshell.ProcessPolicy{RunAsUser: f.Process.RunAsUser, RunAsGroup: f.Process.RunAsGroup}
	}
	return p, nil
}
