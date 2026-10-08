package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	cephHealthRetries  = 30
	cephHealthInterval = 10 * time.Second
)

// cephOSDTree is the subset of `ceph osd tree -f json` the gate reads.
type cephOSDTree struct {
	Nodes []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		Status   string `json:"status"`
		Children []int  `json:"children"`
	} `json:"nodes"`
}

// downOSDsByHost maps each CRUSH host to the names of its OSDs that are not up.
func (t cephOSDTree) downOSDsByHost() map[string][]string {
	hostOf := map[int]string{}
	for _, n := range t.Nodes {
		if n.Type == "host" {
			for _, c := range n.Children {
				hostOf[c] = n.Name
			}
		}
	}
	down := map[string][]string{}
	for _, n := range t.Nodes {
		if n.Type == "osd" && n.Status != "up" {
			down[hostOf[n.ID]] = append(down[hostOf[n.ID]], n.Name)
		}
	}
	return down
}

// cephPGStat is the subset of `ceph pg stat -f json` the gate reads.
type cephPGStat struct {
	PGSummary struct {
		ByState []struct {
			Name string `json:"name"`
			Num  int    `json:"num"`
		} `json:"num_pg_by_state"`
		NumPGs int `json:"num_pgs"`
	} `json:"pg_summary"`
}

func (s cephPGStat) allActiveClean() bool {
	clean := 0
	for _, st := range s.PGSummary.ByState {
		if st.Name == "active+clean" {
			clean += st.Num
		}
	}
	return clean == s.PGSummary.NumPGs
}

// cephJSON runs a ceph command on host (which holds the admin keyring) and
// decodes its JSON output into out.
func cephJSON(r Runner, host Host, args string, out any) error {
	res := SSHRun(r, host, "sudo ceph "+args+" -f json", 30*time.Second)
	if res.Failed() {
		return fmt.Errorf("ceph %s failed: %s", args, strings.TrimSpace(res.Stderr))
	}
	if err := json.Unmarshal([]byte(res.Stdout), out); err != nil {
		return fmt.Errorf("ceph %s: bad JSON: %v", args, err)
	}
	return nil
}

// CephBeforeDeploy refuses to touch host while an OSD on any other host is
// down (two OSDs down at once can leave placement groups without quorum). The
// host's own OSD may already be down: a deploy is often how it gets fixed.
func CephBeforeDeploy(r Runner, host Host) bool {
	fmt.Println("\n  Ceph: checking the other nodes before deploying...")
	var tree cephOSDTree
	if err := cephJSON(r, host, "osd tree", &tree); err != nil {
		fmt.Printf("  ✗ %v\n", err)
		return false
	}
	for h, osds := range tree.downOSDsByHost() {
		if h != host.Name {
			fmt.Printf("  ✗ %s down on %s; deploying %s now would take a second node's OSD down\n",
				strings.Join(osds, ", "), h, host.Name)
			return false
		}
	}
	fmt.Println("  ✓ other nodes' OSDs are up")
	return true
}

// CephAfterDeploy waits for every OSD to be up and every placement group to be
// active+clean, so the next host is not touched while this one is recovering.
func CephAfterDeploy(r Runner, host Host, sleeper func(time.Duration)) bool {
	if sleeper == nil {
		sleeper = time.Sleep
	}
	fmt.Println("  Ceph: waiting for all OSDs up and all PGs active+clean...")
	for i := 0; i < cephHealthRetries; i++ {
		var tree cephOSDTree
		var pgs cephPGStat
		err := cephJSON(r, host, "osd tree", &tree)
		if err == nil {
			err = cephJSON(r, host, "pg stat", &pgs)
		}
		switch {
		case err != nil:
			fmt.Printf("  %v (attempt %d/%d)\n", err, i+1, cephHealthRetries)
		case len(tree.downOSDsByHost()) > 0:
			fmt.Printf("  OSDs down: %v (attempt %d/%d)\n", tree.downOSDsByHost(), i+1, cephHealthRetries)
		case !pgs.allActiveClean():
			fmt.Printf("  PGs not all active+clean (attempt %d/%d)\n", i+1, cephHealthRetries)
		default:
			fmt.Println("  ✓ Ceph healthy")
			return true
		}
		if i < cephHealthRetries-1 {
			sleeper(cephHealthInterval)
		}
	}
	fmt.Printf("  ✗ Ceph did not become healthy after deploying %s\n", host.Name)
	return false
}
