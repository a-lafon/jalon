#!/bin/sh
# Installs the latest release with install.sh, then checks that jalon init and
# the status line work: the steps a new user runs, on an empty HOME. Then checks
# that a corrupted archive is refused and leaves the installed jalon as it was.
# Run by .github/workflows/install.yml, on a machine or in a container.
set -eu

script="$(dirname "$0")/../install.sh"
sh "$script"
# Off the PATH, as on a stock macOS: the path the script prints must work.
"$HOME/.local/bin/jalon" init
grep -q "$HOME/.local/bin/jalon hook prompt" "$HOME/.claude/settings.json"
out=$(echo '{"session_id":"s1","context_window":{"total_input_tokens":230000}}' | "$HOME/.local/bin/jalon" statusline)
echo "$out"
case $out in
"jalon "*) ;;
*) echo "the status line printed no size" >&2 && exit 1 ;;
esac

# A curl first on the PATH, called by install.sh as curl -fsSL -o FILE URL:
# it downloads with the real curl or wget, then appends to any archive.
fake=$(mktemp -d)
real=$(command -v curl || command -v wget)
cat >"$fake/curl" <<EOF
#!/bin/sh
case "$real" in
*curl) "$real" -fsSL -o "\$3" "\$4" ;;
*) "$real" -q -O "\$3" "\$4" ;;
esac
case "\$4" in *.tar.gz) echo x >>"\$3" ;; esac
EOF
chmod 755 "$fake/curl"
before=$(cksum <"$HOME/.local/bin/jalon")
if PATH="$fake:$PATH" sh "$script" 2>"$fake/err"; then
	echo "a corrupted archive was installed" >&2 && exit 1
fi
grep -q "does not match checksums.txt" "$fake/err" || { cat "$fake/err" >&2 && exit 1; }
[ "$(cksum <"$HOME/.local/bin/jalon")" = "$before" ] || { echo "the installed jalon changed" >&2 && exit 1; }
rm -rf "$fake"
echo "corrupted archive refused"
