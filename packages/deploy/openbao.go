package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	openbaoHealthRetries  = 30
	openbaoHealthInterval = 10 * time.Second
	// Each Raft node answers on its loopback listener (modules/services/openbao.nix).
	// These codes make sys/health answer 200 in every state, so the body decides.
	openbaoHealthCmd = "curl -sS -m 5 'http://127.0.0.1:8202/v1/sys/health?standbyok=true&sealedcode=200&uninitcode=200'"
)

// openbaoHealth is the subset of sys/health the gate reads.
type openbaoHealth struct {
	Initialized bool `json:"initialized"`
	Sealed      bool `json:"sealed"`
}

func (h openbaoHealth) serving() bool { return h.Initialized && !h.Sealed }

func openbaoHealthOf(r Runner, host Host) (openbaoHealth, error) {
	var h openbaoHealth
	res := SSHRun(r, host, openbaoHealthCmd, 20*time.Second)
	if res.Failed() {
		return h, fmt.Errorf("sys/health on %s failed: %s", host.Name, strings.TrimSpace(res.Stderr))
	}
	if err := json.Unmarshal([]byte(res.Stdout), &h); err != nil {
		return h, fmt.Errorf("sys/health on %s: bad JSON: %v", host.Name, err)
	}
	return h, nil
}

// openbaoPeers are the other Raft nodes.
func openbaoPeers(host Host) []Host {
	var out []Host
	for _, h := range AllHosts {
		if h.OpenBaoHealthCheck && h.Name != host.Name {
			out = append(out, h)
		}
	}
	return out
}

// OpenBaoBeforeDeploy refuses to touch host unless every other Raft node is
// initialised and unsealed: with one of three already out, taking a second
// loses quorum and stops all issuance. The host itself may be unhealthy: a
// deploy is often how it gets fixed. --force skips this (first rollout, or two
// nodes broken at once; docs/openbao.md).
func OpenBaoBeforeDeploy(r Runner, host Host) bool {
	fmt.Println("\n  OpenBao: checking the other Raft nodes before deploying...")
	for _, p := range openbaoPeers(host) {
		h, err := openbaoHealthOf(r, p)
		switch {
		case err != nil:
			fmt.Printf("  ✗ %v\n", err)
			return false
		case !h.serving():
			fmt.Printf("  ✗ %s is not serving (initialized=%t sealed=%t); deploying %s now could lose quorum\n",
				p.Name, h.Initialized, h.Sealed, host.Name)
			return false
		}
	}
	fmt.Println("  ✓ other Raft nodes are unsealed")
	return true
}

// OpenBaoAfterDeploy waits for host to be initialised and unsealed again, so
// the next node is not touched while this one is still rejoining.
func OpenBaoAfterDeploy(r Runner, host Host, sleeper func(time.Duration)) bool {
	if sleeper == nil {
		sleeper = time.Sleep
	}
	fmt.Println("  OpenBao: waiting for this node to be unsealed...")
	for i := 0; i < openbaoHealthRetries; i++ {
		h, err := openbaoHealthOf(r, host)
		switch {
		case err != nil:
			fmt.Printf("  %v (attempt %d/%d)\n", err, i+1, openbaoHealthRetries)
		case !h.serving():
			fmt.Printf("  initialized=%t sealed=%t (attempt %d/%d)\n", h.Initialized, h.Sealed, i+1, openbaoHealthRetries)
		default:
			fmt.Println("  ✓ OpenBao unsealed")
			return true
		}
		if i < openbaoHealthRetries-1 {
			sleeper(openbaoHealthInterval)
		}
	}
	fmt.Printf("  ✗ OpenBao on %s did not come back unsealed\n", host.Name)
	return false
}
