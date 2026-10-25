package main

import "testing"

const (
	baoServing  = `{"initialized":true,"sealed":false,"standby":true}`
	baoSealed   = `{"initialized":true,"sealed":true,"standby":true}`
	baoUninit   = `{"initialized":false,"sealed":true,"standby":true}`
	baoHealthOn = "127.0.0.1:8202/v1/sys/health"
)

// baoAt answers sys/health for one host; SSH argv carries the host's FQDN.
func baoAt(host, body string) FakeResponse {
	return FakeResponse{Match: MatchContains(cephHost(host).FQDN(), baoHealthOn), Result: RunResult{Stdout: body}}
}

func TestRaftNodesHaveOpenBaoGate(t *testing.T) {
	for _, n := range []string{"fus", "ro", "dah"} {
		if !cephHost(n).OpenBaoHealthCheck {
			t.Errorf("%s: OpenBaoHealthCheck not set", n)
		}
	}
	if got := len(openbaoPeers(cephHost("fus"))); got != 2 {
		t.Errorf("fus has %d peers, want 2", got)
	}
}

func TestOpenBaoBeforeDeployAllowsWhenPeersServe(t *testing.T) {
	fake := &FakeRunner{Responses: []FakeResponse{
		baoAt("ro", baoServing), baoAt("dah", baoServing),
	}}
	if !OpenBaoBeforeDeploy(fake, cephHost("fus")) {
		t.Fatal("expected to proceed: both peers are unsealed")
	}
	if len(fake.CallsContaining(cephHost("fus").FQDN(), baoHealthOn)) != 0 {
		t.Error("the gate should not ask the node being deployed")
	}
}

func TestOpenBaoBeforeDeployRefusesWhenAPeerIsSealed(t *testing.T) {
	fake := &FakeRunner{Responses: []FakeResponse{
		baoAt("ro", baoServing), baoAt("dah", baoSealed),
	}}
	if OpenBaoBeforeDeploy(fake, cephHost("fus")) {
		t.Fatal("expected refusal: dah is sealed")
	}
}

func TestOpenBaoBeforeDeployRefusesWhenAPeerIsUnreachable(t *testing.T) {
	fake := &FakeRunner{Responses: []FakeResponse{
		baoAt("ro", baoServing),
		{Match: MatchContains(cephHost("dah").FQDN(), baoHealthOn), Result: RunResult{ExitCode: 7, Stderr: "connection refused"}},
	}}
	if OpenBaoBeforeDeploy(fake, cephHost("fus")) {
		t.Fatal("expected refusal: dah does not answer")
	}
}

func TestOpenBaoBeforeDeployRefusesUninitialisedPeers(t *testing.T) {
	fake := &FakeRunner{Responses: []FakeResponse{
		baoAt("ro", baoUninit), baoAt("dah", baoUninit),
	}}
	if OpenBaoBeforeDeploy(fake, cephHost("fus")) {
		t.Fatal("expected refusal before init: the first rollout needs --force")
	}
}

func TestOpenBaoAfterDeployWaitsForUnseal(t *testing.T) {
	calls := 0
	fake := &FakeRunner{Responses: []FakeResponse{{
		Match: func(argv []string) bool {
			if !MatchContains(cephHost("ro").FQDN(), baoHealthOn)(argv) {
				return false
			}
			calls++
			return calls <= 2
		},
		Result: RunResult{Stdout: baoSealed},
	}, baoAt("ro", baoServing)}}
	if !OpenBaoAfterDeploy(fake, cephHost("ro"), noSleep) {
		t.Fatal("expected success once ro unseals")
	}
}

func TestOpenBaoAfterDeployGivesUp(t *testing.T) {
	fake := &FakeRunner{Responses: []FakeResponse{baoAt("ro", baoSealed)}}
	if OpenBaoAfterDeploy(fake, cephHost("ro"), noSleep) {
		t.Fatal("expected failure: ro never unseals")
	}
}
