package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// vmHost is a VM on lydia, like browser.
func vmHost(name string) Host {
	return Host{Name: name, FlakeName: name, Parent: "lydia", ConnChecks: []ConnCheck{CheckSSH}}
}

// declares answers the parent eval with names, as `nix eval --json` would.
func declares(names string) FakeResponse {
	return FakeResponse{Match: MatchContains("homelab.incus.vms"), Result: RunResult{Stdout: names + "\n"}}
}

func precheckHosts(t *testing.T, hosts []Host, resps []FakeResponse) ([]PrecheckResult, *FakeRunner) {
	t.Helper()
	fake := &FakeRunner{Responses: resps}
	tops := map[string]Toplevel{}
	for _, h := range hosts {
		tops[h.Name] = Toplevel{Drv: "/nix/store/" + h.Name + ".drv", Out: "/nix/store/" + h.Name}
	}
	return PrecheckAll(fake, hosts, tops, false), fake
}

func resultFor(t *testing.T, pre []PrecheckResult, name string) PrecheckResult {
	t.Helper()
	for _, p := range pre {
		if p.Host.Name == name {
			return p
		}
	}
	t.Fatalf("no precheck result for %s", name)
	return PrecheckResult{}
}

func TestParentVMsArgv(t *testing.T) {
	got := strings.Join(ParentVMsArgv(Host{Name: "lydia", FlakeName: "lydia"}), " ")
	for _, want := range []string{
		".#nixosConfigurations.lydia.config",
		"builtins.attrNames (c.homelab.incus.vms or { })",
		"eval-cache false",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("argv %q lacks %q", got, want)
		}
	}
}

// Rule 6, and the eval runs once per parent however many of its VMs there are.
func TestPrecheckVMDeclaredByParent(t *testing.T) {
	hosts := []Host{vmHost("vm1"), vmHost("vm2")}
	pre, fake := precheckHosts(t, hosts, []FakeResponse{
		declares(`["vm1","vm2"]`),
		{Match: MatchContains("echo ok"), Result: RunResult{Stdout: "ok\n"}},
	})

	for _, p := range pre {
		if p.Skip {
			t.Errorf("%s: a declared VM must not be skipped: %s", p.Host.Name, p.Reason)
		}
	}
	if n := len(fake.CallsContaining("homelab.incus.vms")); n != 1 {
		t.Errorf("want one eval for the shared parent, got %d", n)
	}
}

func TestPrecheckRefusesVMItsParentDoesNotDeclare(t *testing.T) {
	for _, names := range []string{`[]`, `["other"]`} {
		pre, _ := precheckHosts(t, []Host{vmHost("vm1")}, []FakeResponse{
			declares(names),
			{Match: MatchContains("echo ok"), Result: RunResult{Stdout: "ok\n"}},
		})
		p := pre[0]
		if !p.Skip || !p.Failed {
			t.Errorf("declares %s: want Skip and Failed, got Skip=%v Failed=%v", names, p.Skip, p.Failed)
		}
		if !strings.Contains(p.Reason, "does not declare") {
			t.Errorf("declares %s: reason should say the parent does not declare it, got %q", names, p.Reason)
		}
	}
}

func TestPrecheckParentEvalFailureFailsVM(t *testing.T) {
	pre, _ := precheckHosts(t, []Host{vmHost("vm1")}, []FakeResponse{
		{Match: MatchContains("homelab.incus.vms"), Result: RunResult{ExitCode: 1, Stderr: "error: boom\n"}},
		{Match: MatchContains("echo ok"), Result: RunResult{Stdout: "ok\n"}},
	})
	if p := pre[0]; !p.Skip || !p.Failed || !strings.Contains(p.Reason, "boom") {
		t.Errorf("want a failure naming the eval error, got %+v", p)
	}
}

// Hosts without a parent cost no eval.
func TestPrecheckNoParentNoEval(t *testing.T) {
	_, fake := precheckHosts(t, []Host{{Name: "talos", FlakeName: "talos"}}, []FakeResponse{
		{Match: MatchContains("echo ok"), Result: RunResult{Stdout: "ok\n"}},
	})
	if n := len(fake.CallsContaining("nix eval")); n != 0 {
		t.Errorf("want no eval, got %d", n)
	}
}

// Rule 2 in the precheck: the parent's fault is not the VM's, even when the
// VM's own probe failed too (as it would with its parent down).
func TestPrecheckParentFailureSkipsVM(t *testing.T) {
	lydia := Host{Name: "lydia", FlakeName: "lydia"}
	pre, _ := precheckHosts(t, []Host{lydia, vmHost("vm1")}, []FakeResponse{
		declares(`["vm1"]`),
		{Match: MatchContains("echo ok"), Result: RunResult{ExitCode: 255}},
	})

	if p := resultFor(t, pre, "lydia"); !p.Skip || !p.Failed {
		t.Fatalf("lydia: want failed, got %+v", p)
	}
	vm := resultFor(t, pre, "vm1")
	if !vm.Skip || vm.Failed {
		t.Errorf("vm1: want skipped, not failed, got Skip=%v Failed=%v", vm.Skip, vm.Failed)
	}
	if vm.Reason != "parent lydia failed" {
		t.Errorf("vm1: got reason %q", vm.Reason)
	}
}

func TestPrecheckParentSkipSkipsVM(t *testing.T) {
	lydia := Host{Name: "lydia", FlakeName: "lydia", Groups: []string{"printer"}}
	pre, _ := precheckHosts(t, []Host{lydia, vmHost("vm1")}, []FakeResponse{
		declares(`["vm1"]`),
		{Match: MatchContains("curl"), Result: RunResult{ExitCode: 7}},
		{Match: MatchContains("echo ok"), Result: RunResult{Stdout: "ok\n"}},
	})
	vm := resultFor(t, pre, "vm1")
	if !vm.Skip || vm.Failed || vm.Reason != "parent lydia skipped" {
		t.Errorf("vm1: want skipped because of its parent, got %+v", vm)
	}
}

// An undeclared VM is refused even with its parent down: that is a config
// error, whatever the hosts are doing.
func TestPrecheckUndeclaredBeatsParentFailure(t *testing.T) {
	lydia := Host{Name: "lydia", FlakeName: "lydia"}
	pre, _ := precheckHosts(t, []Host{lydia, vmHost("vm1")}, []FakeResponse{
		declares(`[]`),
		{Match: MatchContains("echo ok"), Result: RunResult{ExitCode: 255}},
	})
	if vm := resultFor(t, pre, "vm1"); !vm.Failed || !strings.Contains(vm.Reason, "does not declare") {
		t.Errorf("vm1: want refused, got %+v", vm)
	}
}

func TestWritePlanNestsVMsUnderParent(t *testing.T) {
	pre := []PrecheckResult{
		{Host: Host{Name: "lydia"}, Plan: Plan{SystemPath: "/nix/store/lydia"}},
		{Host: Host{Name: "talos"}, Plan: Plan{UpToDate: true}},
		{Host: vmHost("browser"), Plan: Plan{SystemPath: "/nix/store/browser"}},
	}
	var buf bytes.Buffer
	writePlan(&buf, pre, nil)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	got := lines[len(lines)-3:]
	want := []string{
		"  → lydia         deploy /nix/store/lydia",
		"  → └ browser     deploy /nix/store/browser",
		"  = talos         up to date (no build or copy)",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i, got[i], want[i])
		}
	}
}

// A VM without its parent in the plan is listed at the top level.
func TestWritePlanVMWithoutParentIsTopLevel(t *testing.T) {
	var buf bytes.Buffer
	writePlan(&buf, []PrecheckResult{{Host: vmHost("browser"), Plan: Plan{SystemPath: "/nix/store/browser"}}}, nil)
	if !strings.Contains(buf.String(), "  → browser       deploy") {
		t.Errorf("got %q", buf.String())
	}
}

// vmBootFake makes browser unreachable for a few probes after lydia is told to
// reboot, as an Incus VM is while its host comes back and autostarts it.
type vmBootFake struct {
	*pipeFake
	mu   sync.Mutex
	down int
}

func (f *vmBootFake) Run(ctx context.Context, argv []string, o RunOpts) RunResult {
	name := hostFromArgv(argv, AllHosts)
	last := argv[len(argv)-1]
	f.mu.Lock()
	if name == "lydia" && last == "sudo systemctl reboot" {
		f.down = 2
	}
	if name == "browser" && last == "echo ok" && f.down > 0 {
		f.down--
		f.mu.Unlock()
		return RunResult{ExitCode: 255}
	}
	f.mu.Unlock()
	return f.pipeFake.Run(ctx, argv, o)
}

// Rule 1: a VM ready long before its parent still activates after it. lydia
// is also a build helper, so its own hold must chain into browser's.
func TestPipelineVMActivatesAfterParent(t *testing.T) {
	shortBuildPoll(t)
	f := newPipeFake()
	todo := pipeHosts("lydia", "browser")
	for _, p := range todo {
		f.buildMakes[p.Host.Name] = true
	}
	f.buildDelay = 100 * time.Millisecond
	f.copyDelay["lydia"] = 200 * time.Millisecond

	res := runPipe(f, todo, PipelineOpts{})

	for _, r := range res {
		if r.Status != StatusDeployed {
			t.Errorf("%s: status %v", r.Host.Name, r.Status)
		}
	}
	if strings.Join(f.activeOrder, ",") != "lydia,browser" {
		t.Errorf("want lydia then browser, got %v", f.activeOrder)
	}
}

// Rule 2: the parent's failure skips the VM, under either --on-failure.
func TestPipelineParentFailureSkipsVM(t *testing.T) {
	for _, onFail := range []OnFailure{OnFailureStop, OnFailureContinue} {
		t.Run(string(onFail), func(t *testing.T) {
			f := newPipeFake()
			todo := pipeHosts("lydia", "browser")
			for _, p := range todo {
				f.buildMakes[p.Host.Name] = true
			}
			f.activateFails["lydia"] = true

			res := runPipe(f, todo, PipelineOpts{OnFailure: onFail})

			if got := statusOf(res, "lydia"); got != StatusDeployFailed {
				t.Errorf("lydia: want deploy-failed, got %v", got)
			}
			assertSkippedForParent(t, res, f)
		})
	}
}

// A parent that never builds never reaches activation; its VM is skipped once
// the queue drains.
func TestPipelineParentBuildFailureSkipsVM(t *testing.T) {
	f := newPipeFake()
	todo := pipeHosts("lydia", "browser")
	f.buildMakes["browser"] = true

	res := runPipe(f, todo, PipelineOpts{OnFailure: OnFailureStop})

	if got := statusOf(res, "lydia"); got != StatusBuildFailed {
		t.Errorf("lydia: want build-failed, got %v", got)
	}
	assertSkippedForParent(t, res, f)
}

func assertSkippedForParent(t *testing.T, res []HostResult, f *pipeFake) {
	t.Helper()
	for _, r := range res {
		if r.Host.Name != "browser" {
			continue
		}
		if r.Status != StatusSkipped || r.Status.failed() {
			t.Errorf("browser: want skipped (not a failure), got %v", r.Status)
		}
		if r.Reason != "parent lydia failed" {
			t.Errorf("browser: got reason %q", r.Reason)
		}
	}
	for _, name := range f.activeOrder {
		if name == "browser" {
			t.Error("browser must not be activated after its parent failed")
		}
	}
}

// A parent cancelled by an earlier failure takes its VM with it.
func TestPipelineCancelledParentCancelsVM(t *testing.T) {
	f := newPipeFake()
	todo := pipeHosts("talos", "lydia", "browser")
	for _, p := range todo {
		f.buildMakes[p.Host.Name] = true
	}
	f.activateFails["talos"] = true
	f.copyDelay["lydia"] = 200 * time.Millisecond
	f.copyDelay["browser"] = 200 * time.Millisecond

	res := runPipe(f, todo, PipelineOpts{OnFailure: OnFailureStop})

	for _, name := range []string{"lydia", "browser"} {
		if got := statusOf(res, name); got != StatusCancelled {
			t.Errorf("%s: want cancelled, got %v", name, got)
		}
	}
}

// Rule 4's other half: a VM deployed without its parent does not wait for it.
func TestPipelineVMWithoutParentInRunDeploys(t *testing.T) {
	f := newPipeFake()
	todo := pipeHosts("browser")
	f.buildMakes["browser"] = true

	res := runPipe(f, todo, PipelineOpts{})

	if got := statusOf(res, "browser"); got != StatusDeployed {
		t.Errorf("browser: want deployed, got %v", got)
	}
}

// Rule 3: after the parent reboots, the VM is waited for rather than failed by
// its first SSH check.
func TestPipelineVMWaitsForSSHAfterParentReboot(t *testing.T) {
	f := &vmBootFake{pipeFake: newPipeFake()}
	todo := pipeHosts("lydia", "browser")
	for _, p := range todo {
		f.buildMakes[p.Host.Name] = true
	}
	ctx := pipeCtx(f.pipeFake)
	ctx.Runner = f
	ctx.RebootFlag = RebootFlagAlways

	res := RunPipeline(ctx, f, todo, nil, PipelineOpts{CopyJobs: 4, OnFailure: OnFailureContinue})

	for _, r := range res {
		if r.Status != StatusDeployed {
			t.Errorf("%s: status %v", r.Host.Name, r.Status)
		}
	}
	if f.down != 0 {
		t.Errorf("browser's down probes were never consumed (down=%d): the wait did not run", f.down)
	}
	if strings.Join(f.activeOrder, ",") != "lydia,browser" {
		t.Errorf("want lydia then browser, got %v", f.activeOrder)
	}
}

// Without a parent reboot there is no wait: the first probe is Deploy's own.
func TestAwaitVMAfterParentNoRebootNoProbe(t *testing.T) {
	fake := &FakeRunner{}
	if !awaitVMAfterParent(testCtx(fake), vmHost("browser")) {
		t.Fatal("want true")
	}
	if len(fake.Calls) != 0 {
		t.Errorf("want no calls, got %v", fake.Calls)
	}
}

func TestAwaitVMAfterParentGivesUp(t *testing.T) {
	fake := &FakeRunner{Responses: []FakeResponse{
		{Match: MatchContains("echo ok"), Result: RunResult{ExitCode: 255}},
	}}
	ctx := testCtx(fake)
	now := time.Unix(1700000000, 0)
	ctx.Now = func() time.Time { return now }
	ctx.Sleeper = func(d time.Duration) { now = now.Add(d) }
	ctx.markRebooted("lydia")

	if awaitVMAfterParent(ctx, vmHost("browser")) {
		t.Error("want false for a VM that never answers")
	}
}
