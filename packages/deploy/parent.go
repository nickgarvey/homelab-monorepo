package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// VMs and their parent. A host with Parent set is a VM that runs on that host
// (modules/virtualisation/incus-vms.nix). The rules:
//
//  1. Build and copy stay parallel; the VM's activation waits until the
//     parent's whole deploy (activation, health checks, any reboot) is done.
//  2. Parent failed or skipped: the VM is skipped, not failed.
//  3. Parent rebooted: wait for the VM's SSH, as after a reboot, then deploy.
//  4. Selection is not transitive (SelectHosts).
//  5. The plan nests VMs under their parent (printPlan).
//  6. The parent must declare the VM in homelab.incus.vms (checkParents).

// ParentVMsArgv renders the eval that lists the VMs a parent declares. A
// configuration without the homelab.incus.vms option declares none, so a
// missing option is an empty list rather than an eval error.
func ParentVMsArgv(parent Host) []string {
	return []string{
		"nix", "eval", "--json",
		"--option", "eval-cache", "false",
		".#nixosConfigurations." + parent.FlakeName + ".config",
		"--apply", "c: builtins.attrNames (c.homelab.incus.vms or { })",
	}
}

// parentDecl is what one parent's eval said: the VM names it declares, or why
// they could not be read.
type parentDecl struct {
	vms map[string]bool
	err error
}

// resolveParentVMs evaluates, once per distinct parent of hosts, the VMs that
// parent declares. Parents are looked up in all, not hosts: rule 6 holds
// whether or not the parent is deployed in this run.
func resolveParentVMs(r Runner, all, hosts []Host) map[string]parentDecl {
	var parents []string
	seen := map[string]bool{}
	for _, h := range hosts {
		if h.Parent != "" && !seen[h.Parent] {
			seen[h.Parent] = true
			parents = append(parents, h.Parent)
		}
	}
	out := make(map[string]parentDecl, len(parents))
	if len(parents) == 0 {
		return out
	}

	ctx, cancel := WithTimeout(resolveTimeout)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, name := range parents {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			d := parentDecl{}
			if parent, ok := hostByName(all, name); !ok {
				d.err = fmt.Errorf("%s is not a managed host", name)
			} else if res := r.Run(ctx, ParentVMsArgv(parent), RunOpts{}); res.Failed() {
				d.err = fmt.Errorf("nix eval failed: %s", strings.TrimSpace(lastLines(res.Stderr, 1)))
			} else {
				var names []string
				if err := json.Unmarshal([]byte(res.Stdout), &names); err != nil {
					d.err = fmt.Errorf("could not parse nix eval output: %w", err)
				} else {
					d.vms = make(map[string]bool, len(names))
					for _, n := range names {
						d.vms[n] = true
					}
				}
			}
			mu.Lock()
			out[name] = d
			mu.Unlock()
		}(name)
	}
	wg.Wait()
	return out
}

// checkParents applies rules 6 and 2 to finished precheck results, in place.
//
// A VM its parent does not declare is refused (a failure: the config is wrong
// whatever state the hosts are in). Otherwise a VM whose parent is in this run
// but was skipped or failed by the precheck is skipped, not failed: whatever
// is wrong is the parent's, and the VM's own probe may have failed only
// because its parent is down.
func checkParents(pre []PrecheckResult, decls map[string]parentDecl) {
	index := make(map[string]int, len(pre))
	for i, p := range pre {
		index[p.Host.Name] = i
	}
	for i := range pre {
		h := pre[i].Host
		if h.Parent == "" {
			continue
		}
		d := decls[h.Parent]
		switch {
		case d.err != nil:
			pre[i].Skip, pre[i].Failed = true, true
			pre[i].Reason = fmt.Sprintf("cannot read %s's homelab.incus.vms: %v", h.Parent, d.err)
		case !d.vms[h.Name]:
			pre[i].Skip, pre[i].Failed = true, true
			pre[i].Reason = fmt.Sprintf("parent %s does not declare it in homelab.incus.vms", h.Parent)
		default:
			j, inRun := index[h.Parent]
			if !inRun || !pre[j].Skip {
				continue
			}
			pre[i].Skip, pre[i].Failed = true, false
			if pre[j].Failed {
				pre[i].Reason = fmt.Sprintf("parent %s failed", h.Parent)
			} else {
				pre[i].Reason = fmt.Sprintf("parent %s skipped", h.Parent)
			}
		}
	}
}

// parentBlocks applies rule 2 to a VM whose parent's deploy in this run has
// finished with status parent. blocked means the VM must not be attempted, and
// status and reason are its outcome. A parent cancelled by --on-failure stop
// cancels the VM with it, like every other queued host; a parent that failed
// leaves the VM skipped, which is not counted as a failure.
func parentBlocks(vm Host, parent HostStatus) (status HostStatus, reason string, blocked bool) {
	switch {
	case parent == StatusCancelled:
		return StatusCancelled, fmt.Sprintf("parent %s cancelled", vm.Parent), true
	case parent.failed():
		return StatusSkipped, fmt.Sprintf("parent %s failed", vm.Parent), true
	}
	return StatusDeployed, "", false
}

// awaitVMAfterParent applies rule 3: if this run rebooted the VM's parent, the
// VM went down with it, so wait for its SSH the way the reboot wait does.
// Incus starts the VM again (boot.autostart) only once the parent is up, so
// the parent answering says nothing about the VM.
func awaitVMAfterParent(ctx *DeployContext, vm Host) bool {
	if !ctx.rebooted(vm.Parent) {
		return true
	}
	fmt.Printf("\n  %s: parent %s was rebooted, waiting for %s\n", vm.Name, vm.Parent, vm.FQDN())
	// The precheck's master to the VM went down with it.
	dropSSHMaster(ctx.Runner, vm)
	return waitBackOnline(ctx, vm)
}

func hostByName(all []Host, name string) (Host, bool) {
	for _, h := range all {
		if h.Name == name {
			return h, true
		}
	}
	return Host{}, false
}
