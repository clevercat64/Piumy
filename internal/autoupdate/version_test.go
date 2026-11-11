package autoupdate

import "testing"

func TestVersionFromTag(t *testing.T) {
	good := map[string]string{"v0.12.5": "0.12.5", "v1.0.0": "1.0.0", "v10.20.30": "10.20.30"}
	for tag, want := range good {
		if got, ok := versionFromTag(tag); !ok || got != want {
			t.Errorf("versionFromTag(%q) = %q, %v, want %q", tag, got, ok, want)
		}
	}
	// None of these is a release this updater may act on.
	for _, tag := range []string{"", "0.12.5", "v0.12", "v0.12.5.1", "v0.12.5-rc1", "V0.12.5", "vx.y.z", "v1234567.0.0", "main", "v0.12.5\n"} {
		if got, ok := versionFromTag(tag); ok {
			t.Errorf("versionFromTag(%q) = %q, want it refused", tag, got)
		}
	}
}

// Numeric, not alphabetical: 0.9.10 comes after 0.9.9. Equal or older, or
// anything that is not a plain x.y.z, is never "newer".
func TestNewerIsStrictlyNumeric(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.12.6", "0.12.5", true},
		{"0.13.0", "0.12.9", true},
		{"1.0.0", "0.99.99", true},
		{"0.9.10", "0.9.9", true},
		{"0.12.5", "0.12.5", false},
		{"0.12.4", "0.12.5", false},
		{"0.9.9", "0.9.10", false},
		{"0.12.6", "dev", false},
		{"", "0.12.5", false},
		{"0.12.6", "", false},
		{"0.12.6-rc1", "0.12.5", false},
		{"v0.12.6", "0.12.5", false},
	}
	for _, c := range cases {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// The names are literals taken from the real 0.12.5 release's asset list, not
// produced by the same function — a typo in the function can't pass itself.
func TestPackageNameMatchesTheRealReleaseAssets(t *testing.T) {
	cases := []struct{ goos, goarch, want string }{
		{"windows", "amd64", "Piumy-Setup-0.12.5.exe"},
		{"linux", "amd64", "Piumy-0.12.5-linux-amd64.tar.gz"},
		{"linux", "arm64", "Piumy-0.12.5-linux-arm64.tar.gz"},
		{"linux", "arm", "Piumy-0.12.5-linux-armv7.tar.gz"},
		{"darwin", "amd64", "Piumy-0.12.5-darwin-amd64.tar.gz"},
		{"darwin", "arm64", "Piumy-0.12.5-darwin-arm64.tar.gz"},
	}
	for _, c := range cases {
		if got, ok := packageName(c.goos, c.goarch, "0.12.5"); !ok || got != c.want {
			t.Errorf("packageName(%s, %s) = %q, %v, want %q", c.goos, c.goarch, got, ok, c.want)
		}
	}
	for _, c := range []struct{ goos, goarch string }{
		{"windows", "arm64"}, {"windows", "386"}, {"linux", "386"}, {"linux", "riscv64"}, {"darwin", "arm"}, {"freebsd", "amd64"},
	} {
		if got, ok := packageName(c.goos, c.goarch, "0.12.5"); ok {
			t.Errorf("packageName(%s, %s) = %q: there is no such package, it must say so", c.goos, c.goarch, got)
		}
	}
}
