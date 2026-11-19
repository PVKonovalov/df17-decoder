// Package version reports the program version: set at link time by `make build`
// (-X .../internal/version.Version=$(git describe --tags --always --dirty)), otherwise taken from the
// build info Go embeds (module version for `go install …@vX`, VCS revision for `go build` in a checkout).
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set with -ldflags "-X github.com/PVKonovalov/df17-decoder/internal/version.Version=v1.2.3 -X ….Commit=abc1234".
var (
	Version string
	Commit  string
)

// Info is the resolved version of the running binary.
type Info struct {
	Version   string `json:"version"`             // git tag, e.g. "v1.0.0" or "v1.0.0-3-g4a2b4d4-dirty"; "dev" if unknown
	Commit    string `json:"commit,omitempty"`    // short VCS revision, "" if unknown
	GoVersion string `json:"goVersion,omitempty"` // toolchain that built the binary
}

// Get returns the version from the linker flags, falling back to the embedded build info.
func Get() Info {
	bi, _ := debug.ReadBuildInfo()
	return resolve(Version, Commit, bi)
}

// resolve combines linker-set values (version, commit) with build info bi (may be nil).
func resolve(version, commit string, bi *debug.BuildInfo) Info {
	info := Info{Version: version, Commit: commit, GoVersion: runtime.Version()}
	if bi == nil {
		if info.Version == "" {
			info.Version = "dev"
		}
		return info
	}
	info.GoVersion = bi.GoVersion
	var rev string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if info.Commit == "" && rev != "" {
		info.Commit = rev[:min(len(rev), 7)]
	}
	if info.Version == "" {
		switch v := bi.Main.Version; {
		case v != "" && v != "(devel)":
			info.Version = v
		case info.Commit != "":
			info.Version = "dev-" + info.Commit
			if modified {
				info.Version += "-dirty"
			}
		default:
			info.Version = "dev"
		}
	}
	return info
}

// String is the one-line form printed by -version, e.g. "v1.0.0 (commit 4a2b4d4, go1.26.5, linux/arm64)".
func (i Info) String() string {
	s := i.Version + " ("
	if i.Commit != "" {
		s += "commit " + i.Commit + ", "
	}
	return s + fmt.Sprintf("%s, %s/%s)", i.GoVersion, runtime.GOOS, runtime.GOARCH)
}
