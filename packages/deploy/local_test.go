package main

import (
	"strings"
	"testing"
)

// machineIDResps answers `cat /etc/machine-id` with id, ahead of the
// happy-path responses.
func machineIDResps(id string) []FakeResponse {
	return append([]FakeResponse{
		{Match: MatchContains("/etc/machine-id"), Result: RunResult{Stdout: id + "\n"}},
	}, buildOKResponses(fakeSystemPath)...)
}

// kernelChangedResps is machineIDResps with a kernel change, so detection says
// a reboot is owed.
func kernelChangedResps(id string) []FakeResponse {
	return append([]FakeResponse{
		{Match: MatchContains("kernel-modules"), Result: RunResult{Stdout: "6.6.99\n"}},
	}, machineIDResps(id)...)
}

// assertNoReboot fails if anything could reboot the host: neither the
// watchdog timer nor a direct reboot. Both contain "systemctl reboot".
func assertNoReboot(t *testing.T, fake *FakeRunner) {
	t.Helper()
	if calls := fake.CallsContaining("systemctl reboot"); len(calls) > 0 {
		t.Errorf("host was set up to reboot: %s", CallString(calls[0]))
	}
}

func TestLocalSafeDeployArmsNoWatchdogAndNeverReboots(t *testing.T) {
	for _, flag := range []RebootFlag{RebootFlagNever, RebootFlagAuto, RebootFlagAlways, RebootFlagAsk} {
		t.Run(string(flag), func(t *testing.T) {
			host := AllHosts[1]
			host.K8sHealthCheck = false
			fake := &FakeRunner{Responses: kernelChangedResps(fakeLocalMachineID)}
			var warnings []string
			ctx := testCtx(fake)
			ctx.RebootFlag = flag
			ctx.Warnings = &warnings
			ctx.Prompter = func(string) bool {
				t.Error("--reboot ask must not prompt to reboot the local machine")
				return true
			}

			if !Deploy(ctx, host, ModeSafe, Plan{SystemPath: fakeSystemPath}) {
				t.Fatal("expected success")
			}
			assertNoReboot(t, fake)
			// Still activated and persisted: only the reboots are withheld.
			assertOrder(t, joinedCalls(fake), []string{
				"switch-to-configuration test",
				"switch-to-configuration boot",
			})
			if len(warnings) != 1 || !strings.Contains(warnings[0], "reboot") {
				t.Errorf("want one warning that a reboot is owed, got %q", warnings)
			}
		})
	}
}

func TestLocalBootModeRebootAlwaysDoesNotReboot(t *testing.T) {
	host := AllHosts[1]
	host.K8sHealthCheck = false
	fake := &FakeRunner{Responses: machineIDResps(fakeLocalMachineID)}
	ctx := testCtx(fake)
	ctx.RebootFlag = RebootFlagAlways

	if !Deploy(ctx, host, ModeBoot, Plan{SystemPath: fakeSystemPath}) {
		t.Fatal("expected success")
	}
	assertNoReboot(t, fake)
}

func TestLocalAlreadyDeployedOwedRebootDoesNotReboot(t *testing.T) {
	host := AllHosts[1]
	host.K8sHealthCheck = false
	fake := &FakeRunner{Responses: append([]FakeResponse{
		{Match: MatchContains("readlink", "profiles/system"), Result: RunResult{Stdout: fakeSystemPath + "\n"}},
	}, kernelChangedResps(fakeLocalMachineID)...)}
	ctx := testCtx(fake)
	ctx.RebootFlag = RebootFlagAuto

	if !Deploy(ctx, host, ModeSafe, Plan{SystemPath: fakeSystemPath}) {
		t.Fatal("expected success")
	}
	assertNoReboot(t, fake)
}

// Without a local machine ID nothing can be ruled out, so the safe flow stops
// before activating rather than arm a watchdog that might fire here.
func TestUnknownLocalIDAbortsBeforeActivation(t *testing.T) {
	host := AllHosts[1]
	host.K8sHealthCheck = false
	fake := &FakeRunner{Responses: buildOKResponses(fakeSystemPath)}
	ctx := testCtx(fake)
	ctx.LocalMachineID = ""

	if Deploy(ctx, host, ModeSafe, Plan{SystemPath: fakeSystemPath}) {
		t.Fatal("expected failure")
	}
	assertNoReboot(t, fake)
	if contains(joinedCalls(fake), "switch-to-configuration test") {
		t.Error("activated without knowing whether the target is local")
	}
}

func TestUnreadableRemoteIDBlocksReboot(t *testing.T) {
	host := AllHosts[1]
	host.K8sHealthCheck = false
	fake := &FakeRunner{Responses: machineIDResps("")}
	ctx := testCtx(fake)
	ctx.RebootFlag = RebootFlagAlways

	if !Deploy(ctx, host, ModeBoot, Plan{SystemPath: fakeSystemPath}) {
		t.Fatal("expected success")
	}
	assertNoReboot(t, fake)
}
