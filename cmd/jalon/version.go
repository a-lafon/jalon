package main

import (
	"fmt"
	"io"
	"runtime/debug"
)

// version, commit and date are set by the release build:
// -ldflags "-X main.version=0.1.0 -X main.commit=8b7269d -X main.date=2026-10-11T09:00:00Z".
var version, commit, date string

// runVersion runs "jalon version": one line saying which build this is.
func runVersion(out io.Writer) {
	info, _ := debug.ReadBuildInfo()
	fmt.Fprintln(out, versionLine(version, commit, date, info))
}

// versionLine is "jalon <version> (<commit>, <date>)". Each value is the one the
// release build injected, else the one info, the binary's build information
// (nil when there is none), records, else dev or unknown.
func versionLine(version, commit, date string, info *debug.BuildInfo) string {
	commit = shortCommit(commit)
	if info != nil {
		if version == "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		vcs := map[string]string{}
		for _, s := range info.Settings {
			vcs[s.Key] = s.Value
		}
		if commit == "" && vcs["vcs.revision"] != "" {
			commit = shortCommit(vcs["vcs.revision"])
			if vcs["vcs.modified"] == "true" {
				commit += "-dirty"
			}
		}
		if date == "" {
			date = vcs["vcs.time"]
		}
	}
	if version == "" {
		version = "dev"
	}
	if commit == "" {
		commit = "unknown"
	}
	if date == "" {
		date = "unknown"
	}
	return fmt.Sprintf("jalon %s (%s, %s)", version, commit, date)
}

// shortCommit is the first 7 characters of a commit hash.
func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}
