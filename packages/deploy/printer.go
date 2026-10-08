package main

import (
	"encoding/json"
	"time"
)

// Klipper's print_stats.state values. "printing" and "paused" mean a job is
// active and a deploy (which restarts klipper) would interrupt it. The rest
// (standby, complete, cancelled, error) are safe to deploy through.
var activePrintStates = map[string]bool{
	"printing": true,
	"paused":   true,
}

type moonrakerPrintStatsResp struct {
	Result struct {
		Status struct {
			PrintStats struct {
				State string `json:"state"`
			} `json:"print_stats"`
		} `json:"status"`
	} `json:"result"`
}

// CheckPrinterIdle queries moonraker's print_stats on `host` and reports
// whether it is safe to deploy. Returns (idle, state, queryOK). If queryOK
// is false, moonraker was unreachable or returned garbage — caller decides
// whether to abort or proceed (we currently abort to fail closed: an
// unreachable moonraker on a printer host most likely means klipper is
// already wedged, but it could also mean the printer is mid-job with a
// network blip, and we shouldn't guess).
func CheckPrinterIdle(r Runner, host Host) (idle bool, state string, queryOK bool) {
	// Moonraker listens on the printer host's loopback only; everything else
	// goes through nginx, which wants an Authelia login or the API key. So ask
	// it from the host itself, over SSH.
	res := SSHRun(r, host,
		"curl -fsS --max-time 5 'http://[::1]:7125/printer/objects/query?print_stats'",
		15*time.Second)
	if res.Failed() {
		return false, "", false
	}
	var parsed moonrakerPrintStatsResp
	if err := json.Unmarshal([]byte(res.Stdout), &parsed); err != nil {
		return false, "", false
	}
	state = parsed.Result.Status.PrintStats.State
	if state == "" {
		return false, "", false
	}
	return !activePrintStates[state], state, true
}
