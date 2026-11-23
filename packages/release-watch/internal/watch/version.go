package watch

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a release version: major.minor.patch with an optional
// prerelease suffix.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// ParseVersion reads "4.39.28", "v4.39.28" or "4.40.0-beta1". A missing
// patch or minor is 0. Leading comparison operators, as some advisories put
// in patched_versions (">4.28.0"), are dropped.
func ParseVersion(s string) (Version, error) {
	t := strings.TrimLeft(strings.TrimSpace(s), "<>=~^ ")
	t = strings.TrimPrefix(t, "v")
	var v Version
	core, pre, _ := strings.Cut(t, "-")
	core, _, _ = strings.Cut(core, "+")
	v.Pre = pre
	parts := strings.Split(core, ".")
	if len(parts) > 3 || parts[0] == "" {
		return Version{}, fmt.Errorf("version %q: not major.minor.patch", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("version %q: not major.minor.patch", s)
		}
		switch i {
		case 0:
			v.Major = n
		case 1:
			v.Minor = n
		case 2:
			v.Patch = n
		}
	}
	return v, nil
}

// Compare is -1, 0 or 1 as v is older than, the same as or newer than w. A
// prerelease is older than its release; prereleases compare as strings.
func (v Version) Compare(w Version) int {
	for _, d := range []int{v.Major - w.Major, v.Minor - w.Minor, v.Patch - w.Patch} {
		if d != 0 {
			return sign(d)
		}
	}
	switch {
	case v.Pre == w.Pre:
		return 0
	case v.Pre == "":
		return 1
	case w.Pre == "":
		return -1
	}
	return strings.Compare(v.Pre, w.Pre)
}

func sign(d int) int {
	if d < 0 {
		return -1
	}
	return 1
}

// ImageTag is the tag of an image reference ("ghcr.io/authelia/authelia:4.39.28"
// gives "4.39.28"), ignoring a digest.
func ImageTag(image string) (string, error) {
	ref, _, _ := strings.Cut(image, "@")
	name := ref[strings.LastIndex(ref, "/")+1:]
	_, tag, ok := strings.Cut(name, ":")
	if !ok || tag == "" {
		return "", fmt.Errorf("image %q has no tag", image)
	}
	return tag, nil
}
