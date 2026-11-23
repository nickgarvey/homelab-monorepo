package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GitHub reads releases and published security advisories from GitHub's
// REST API, unauthenticated: two requests per target per run, well inside
// the 60 an hour GitHub allows an address.
type GitHub struct {
	Client  *http.Client
	BaseURL string // https://api.github.com
}

// Release is a GitHub release.
type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// Advisory is a repository security advisory.
type Advisory struct {
	GHSAID          string          `json:"ghsa_id"`
	Severity        string          `json:"severity"`
	State           string          `json:"state"`
	WithdrawnAt     *string         `json:"withdrawn_at"`
	HTMLURL         string          `json:"html_url"`
	Vulnerabilities []Vulnerability `json:"vulnerabilities"`
}

// Vulnerability is one affected package of an advisory. PatchedVersions is
// free text: "4.39.20", "4.38.19,4.39.0", "v4.29.3", ">4.28.0".
type Vulnerability struct {
	PatchedVersions string `json:"patched_versions"`
}

// LatestRelease is the newest release of repo ("owner/name") that is not a
// draft or a prerelease.
func (g *GitHub) LatestRelease(ctx context.Context, repo string) (Release, error) {
	var r Release
	err := g.get(ctx, "/repos/"+repo+"/releases/latest", &r)
	if err == nil && r.TagName == "" {
		err = fmt.Errorf("%s: latest release has no tag", repo)
	}
	return r, err
}

// Advisories is repo's published security advisories, the first 100: no
// repository watched here has come near that many.
func (g *GitHub) Advisories(ctx context.Context, repo string) ([]Advisory, error) {
	var as []Advisory
	err := g.get(ctx, "/repos/"+repo+"/security-advisories?state=published&per_page=100", &as)
	return as, err
}

func (g *GitHub) get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "release-watch")
	resp, err := g.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: %s (rate limit remaining %q): %s", path, resp.Status,
			resp.Header.Get("X-RateLimit-Remaining"), body)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	return nil
}
