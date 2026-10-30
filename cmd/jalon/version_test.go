package main

import (
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"testing"
)

func TestVersionLine(t *testing.T) {
	checkout := &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "8b7269dfaeaa063be6144eaab7ed91a24cf524cd"},
			{Key: "vcs.time", Value: "2026-10-10T09:23:27Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	tests := map[string]struct {
		version, commit, date string
		info                  *debug.BuildInfo
		want                  string
	}{
		"release build": {
			"0.1.0", "8b7269d", "2026-10-11T09:00:00Z", checkout,
			"jalon 0.1.0 (8b7269d, 2026-10-11T09:00:00Z)",
		},
		"modified checkout": {
			"", "", "", checkout,
			"jalon dev (8b7269d-dirty, 2026-10-10T09:23:27Z)",
		},
		"clean checkout": {
			"", "", "", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "8b7269dfaeaa"}, {Key: "vcs.modified", Value: "false"},
			}},
			"jalon dev (8b7269d, unknown)",
		},
		"go install at a tag": {
			"", "", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}},
			"jalon v0.1.0 (unknown, unknown)",
		},
		"no build information": {
			"", "", "", nil,
			"jalon dev (unknown, unknown)",
		},
		"long injected commit": {
			"0.1.0", "8b7269dfaeaa063be6144eaab7ed91a24cf524cd", "2026-10-11T09:00:00Z", nil,
			"jalon 0.1.0 (8b7269d, 2026-10-11T09:00:00Z)",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := versionLine(tt.version, tt.commit, tt.date, tt.info); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The variables the release build sets are the ones jalon version prints, under
// both spellings of the command.
func TestVersionCommandPrintsTheInjectedValues(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "jalon")
	ldflags := "-X main.version=0.1.0 -X main.commit=8b7269d -X main.date=2026-10-11T09:00:00Z"
	if out, err := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	for _, arg := range []string{"version", "--version"} {
		out, err := exec.Command(bin, arg).CombinedOutput()
		if err != nil {
			t.Errorf("jalon %s: %v", arg, err)
		}
		if want := "jalon 0.1.0 (8b7269d, 2026-10-11T09:00:00Z)\n"; string(out) != want {
			t.Errorf("jalon %s = %q, want %q", arg, out, want)
		}
	}
}

// An advice records the version the binary says it is, not the module version
// of the build information, which a release build sets to a pseudo-version.
func TestAnAdviceRecordsTheVersionTheBinaryWasBuiltWith(t *testing.T) {
	released := &debug.BuildInfo{Main: debug.Module{Version: "v0.5.1-0.20261030154334-c0b0fc5f4c32"}}
	if got := binaryVersion("0.5.1", released); got != "0.5.1" {
		t.Errorf("released build = %q, want the injected 0.5.1", got)
	}
	// The report's "given by version" line and jalon version must say the same.
	if line := versionLine("0.5.1", "", "", released); line != "jalon 0.5.1 (unknown, unknown)" {
		t.Errorf("version line = %q, want the same 0.5.1", line)
	}
	if got := binaryVersion("", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}); got != "" {
		t.Errorf("checkout build = %q, want no version", got)
	}
}
