package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	vcs := func(rev, modified, mainVersion string) *debug.BuildInfo {
		return &debug.BuildInfo{GoVersion: "go1.26.5", Main: debug.Module{Version: mainVersion}, Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: modified},
		}}
	}
	tests := []struct {
		name            string
		version, commit string
		bi              *debug.BuildInfo
		want            Info
	}{
		{"ldflags win", "v1.2.0", "abc1234", vcs("4a2b4d4e5f", "true", "v9.9.9"), Info{"v1.2.0", "abc1234", "go1.26.5"}},
		{"ldflags version, commit from vcs", "v1.2.0", "", vcs("4a2b4d4e5f", "false", "(devel)"), Info{"v1.2.0", "4a2b4d4", "go1.26.5"}},
		{"go install @tag", "", "", &debug.BuildInfo{GoVersion: "go1.26.5", Main: debug.Module{Version: "v1.0.0"}}, Info{"v1.0.0", "", "go1.26.5"}},
		{"go build in a checkout", "", "", vcs("4a2b4d4e5f", "false", "(devel)"), Info{"dev-4a2b4d4", "4a2b4d4", "go1.26.5"}},
		{"go build with local changes", "", "", vcs("4a2b4d4e5f", "true", "(devel)"), Info{"dev-4a2b4d4-dirty", "4a2b4d4", "go1.26.5"}},
		{"nothing known", "", "", &debug.BuildInfo{GoVersion: "go1.26.5", Main: debug.Module{Version: "(devel)"}}, Info{"dev", "", "go1.26.5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolve(tt.version, tt.commit, tt.bi); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
	if got := resolve("", "", nil); got.Version != "dev" {
		t.Errorf("no build info: %+v", got)
	}
}

func TestString(t *testing.T) {
	s := Info{Version: "v1.0.0", Commit: "4a2b4d4", GoVersion: "go1.26.5"}.String()
	if !strings.HasPrefix(s, "v1.0.0 (commit 4a2b4d4, go1.26.5, ") || !strings.HasSuffix(s, ")") {
		t.Errorf("String() = %q", s)
	}
	if s := (Info{Version: "dev", GoVersion: "go1.26.5"}).String(); !strings.HasPrefix(s, "dev (go1.26.5, ") {
		t.Errorf("String() without commit = %q", s)
	}
}
