// Package watch compares the version of a pinned image running in the
// cluster with the newest GitHub release of its project, and finds the
// published security advisories that upgrading would fix.
package watch

import (
	"context"
	"fmt"
	"strings"
)

// Target is a hand-bumped image: where its pods run, and its GitHub repo.
// Each target's namespace needs the Role in
// k8s/manifests/release-watch/rbac.yaml.
type Target struct {
	Name      string // the metrics' label name
	Namespace string
	Selector  string // label selector of its pods
	Container string
	Repo      string // owner/name on GitHub
}

// Targets is what ReleaseWatch checks.
var Targets = []Target{
	{Name: "authelia", Namespace: "authelia", Selector: "app=authelia", Container: "authelia", Repo: "authelia/authelia"},
}

// TargetResult is what Check found for one target.
type TargetResult struct {
	Name, Repo string
	Running    string // the oldest version among its running pods
	Latest     string // the newest release's tag
	LatestURL  string
	Behind     bool
	// Fixes are the published advisories that affect Running and are
	// patched in a release up to Latest.
	Fixes []Fix `json:",omitempty"`
}

// Fix is an advisory that upgrading fixes.
type Fix struct {
	ID, Severity, Patched, URL string
}

// Activities are ReleaseWatch's activities.
type Activities struct {
	GitHub *GitHub
	Kube   interface {
		RunningImages(ctx context.Context, namespace, selector, container string) ([]string, error)
	}
}

// Check compares t's running version with its latest release.
func (a *Activities) Check(ctx context.Context, t Target) (TargetResult, error) {
	r := TargetResult{Name: t.Name, Repo: t.Repo}
	images, err := a.Kube.RunningImages(ctx, t.Namespace, t.Selector, t.Container)
	if err != nil {
		return r, err
	}
	if len(images) == 0 {
		return r, fmt.Errorf("no running pod in %s matches %s with a container %s", t.Namespace, t.Selector, t.Container)
	}
	var running Version
	for i, img := range images {
		tag, err := ImageTag(img)
		if err != nil {
			return r, err
		}
		v, err := ParseVersion(tag)
		if err != nil {
			return r, fmt.Errorf("image %s: %w", img, err)
		}
		if i == 0 || v.Compare(running) < 0 {
			running = v
		}
	}
	rel, err := a.GitHub.LatestRelease(ctx, t.Repo)
	if err != nil {
		return r, err
	}
	latest, err := ParseVersion(rel.TagName)
	if err != nil {
		return r, fmt.Errorf("%s latest release: %w", t.Repo, err)
	}
	advs, err := a.GitHub.Advisories(ctx, t.Repo)
	if err != nil {
		return r, err
	}
	r.Running, r.Latest, r.LatestURL = running.String(), rel.TagName, rel.HTMLURL
	r.Behind = running.Compare(latest) < 0
	r.Fixes = Fixes(running, latest, advs)
	return r, nil
}

// Fixes is the advisories that affect running and have a patched version
// newer than running and no newer than latest. Advisories list one patched
// version per maintained line ("4.38.19,4.39.0"), so running counts as
// patched if a patched version on its own major.minor line is not newer
// than it, or if it is at least the newest patched version. Patched versions
// that do not parse are skipped.
func Fixes(running, latest Version, advs []Advisory) []Fix {
	var fixes []Fix
	for _, a := range advs {
		if a.State != "published" || a.WithdrawnAt != nil {
			continue
		}
		var patched []Version
		for _, vuln := range a.Vulnerabilities {
			for _, s := range strings.Split(vuln.PatchedVersions, ",") {
				if v, err := ParseVersion(s); err == nil {
					patched = append(patched, v)
				}
			}
		}
		if len(patched) == 0 {
			continue
		}
		var newest Version
		var fix *Version
		affected := true
		for i, p := range patched {
			if i == 0 || p.Compare(newest) > 0 {
				newest = p
			}
			if p.Compare(running) <= 0 && p.Major == running.Major && p.Minor == running.Minor {
				affected = false
			}
			if p.Compare(running) > 0 && p.Compare(latest) <= 0 && (fix == nil || p.Compare(*fix) < 0) {
				fix = &patched[i]
			}
		}
		if newest.Compare(running) <= 0 {
			affected = false
		}
		if affected && fix != nil {
			fixes = append(fixes, Fix{ID: a.GHSAID, Severity: a.Severity, Patched: fix.String(), URL: a.HTMLURL})
		}
	}
	return fixes
}
