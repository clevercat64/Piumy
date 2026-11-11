package autoupdate

import (
	"regexp"
	"strconv"
)

var (
	versionRE = regexp.MustCompile(`^(\d{1,6})\.(\d{1,6})\.(\d{1,6})$`)
	tagRE     = regexp.MustCompile(`^v(\d{1,6}\.\d{1,6}\.\d{1,6})$`)
)

// versionFromTag turns "v0.12.5" into "0.12.5"; anything else is not a release
// tag this updater will act on (a pre-release suffix, a branch name…).
func versionFromTag(tag string) (string, bool) {
	m := tagRE.FindStringSubmatch(tag)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// newer reports whether version a is strictly newer than b. Both must be plain
// x.y.z: if either is not, the answer is no — an update is never installed on a
// guess, and never when the versions are equal or go backwards.
func newer(a, b string) bool {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return false
	}
	for i := range av {
		if av[i] != bv[i] {
			return av[i] > bv[i]
		}
	}
	return false
}

func parseVersion(s string) (v [3]int, ok bool) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return v, false
	}
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

// packageName is the release asset this platform installs from — built here,
// never matched by a wildcard: Windows runs the Setup, Linux and macOS the
// tar.gz with install.sh (T113). arm is the only 32-bit build published, v7.
func packageName(goos, goarch, version string) (string, bool) {
	switch goos {
	case "windows":
		if goarch == "amd64" {
			return "Piumy-Setup-" + version + ".exe", true
		}
	case "linux":
		switch goarch {
		case "amd64", "arm64":
			return "Piumy-" + version + "-linux-" + goarch + ".tar.gz", true
		case "arm":
			return "Piumy-" + version + "-linux-armv7.tar.gz", true
		}
	case "darwin":
		if goarch == "amd64" || goarch == "arm64" {
			return "Piumy-" + version + "-darwin-" + goarch + ".tar.gz", true
		}
	}
	return "", false
}
