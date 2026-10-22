#!/bin/sh
# Installs the jalon binary from a GitHub release, without Homebrew:
#
#   curl -fsSL https://raw.githubusercontent.com/a-lafon/jalon/main/install.sh | sh
#
# It downloads the archive for this OS and architecture, checks it against the
# release's checksums.txt, and puts jalon in JALON_INSTALL_DIR (default
# ~/.local/bin). JALON_VERSION picks a release tag, such as v0.4.0 (default: the
# latest). It never runs jalon init: that is yours to run.
#
# A file fetched with curl or wget is not quarantined by macOS, so the unsigned
# binary runs without clearing that mark.

set -eu

repo=a-lafon/jalon

fail() {
	echo "jalon install: $*" >&2
	exit 1
}

# fetch URL FILE downloads with curl or, without it, wget (busybox included).
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		fail "needs curl or wget"
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		fail "needs sha256sum or shasum to check the download"
	fi
}

# The whole script runs from main, so a download cut short runs nothing.
main() {
	case $(uname -s) in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) fail "no release for $(uname -s): build it with go install, see https://github.com/$repo/blob/main/docs/install.md" ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "no release for $(uname -m)" ;;
	esac

	version=${JALON_VERSION:-latest}
	if [ "$version" = latest ]; then
		base=https://github.com/$repo/releases/latest/download
	else
		base=https://github.com/$repo/releases/download/$version
	fi
	dir=${JALON_INSTALL_DIR:-$HOME/.local/bin}

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT

	fetch "$base/checksums.txt" "$tmp/checksums.txt" || fail "cannot download $base/checksums.txt"
	# The archive's name holds the version, so it is read from checksums.txt:
	# "latest" then needs no API call to know which version it is.
	line=$(grep "_${os}_${arch}\.tar\.gz\$" "$tmp/checksums.txt") || fail "no ${os}_${arch} archive in $base/checksums.txt"
	want=${line%% *}
	archive=${line##* }

	fetch "$base/$archive" "$tmp/$archive" || fail "cannot download $base/$archive"
	got=$(sha256 "$tmp/$archive")
	[ "$got" = "$want" ] || fail "$archive does not match checksums.txt: got $got, want $want"

	tar -xzf "$tmp/$archive" -C "$tmp" jalon
	mkdir -p "$dir"
	# Copied next to its target, then renamed: a rename within one directory
	# never leaves a half-written jalon for a hook to run.
	cp "$tmp/jalon" "$dir/.jalon.new"
	chmod 755 "$dir/.jalon.new"
	mv -f "$dir/.jalon.new" "$dir/jalon"

	echo "Installed $("$dir/jalon" version) in $dir/jalon"
	# Off the PATH, the full path still works: jalon init records it in the hooks.
	run=jalon
	case ":$PATH:" in
	*":$dir:"*) ;;
	*)
		run=$dir/jalon
		echo "$dir is not on your PATH: add it to run jalon by its name."
		;;
	esac
	echo "Next: run $run init to add the hooks and the status line to Claude Code."
}

main
