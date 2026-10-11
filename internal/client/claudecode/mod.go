package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/a-lafon/jalon/internal/client"
)

var _ client.ModInstaller = Adapter{}

const (
	// modMarketplace is the repository Claude Code installs the mod from: its
	// marketplace file lists the mod, in plugin/.
	modMarketplace = "a-lafon/jalon"
	// modName is the mod's name and its marketplace's, as Claude Code records
	// them: the mod is jalon@jalon.
	modName = "jalon"
)

// modMinVersion is the first Claude Code whose CLI adds a marketplace and
// installs from it in one command; it runs mods too.
var modMinVersion = [3]int{2, 1, 292}

// CheckMod reads `claude --version` and refuses a Claude Code that cannot
// install the mod, or that is not found.
func (Adapter) CheckMod(run client.Runner) error {
	out, err := run("claude", "--version")
	if err != nil {
		return fmt.Errorf("the mod is installed with the claude command, which did not run: %w; use the status line", err)
	}
	version, ok := parseVersion(string(out))
	if !ok {
		return fmt.Errorf("cannot read the Claude Code version from %q; use the status line", strings.TrimSpace(string(out)))
	}
	if compareVersions(version, modMinVersion) < 0 {
		return fmt.Errorf("the mod needs Claude Code %s or later, this is %s; update Claude Code, or use the status line",
			formatVersion(modMinVersion), formatVersion(version))
	}
	return nil
}

// InstallMod installs the mod from its marketplace, adding the marketplace when
// it is missing, with its binary option set to exe. Run again, it keeps the mod
// and updates the option.
func (Adapter) InstallMod(run client.Runner, exe string, dryRun bool) (string, error) {
	args := []string{"plugin", "install", modName, "--marketplace", modMarketplace, "--config", "binary=" + exe}
	if dryRun {
		return "mod: would run claude " + strings.Join(args, " "), nil
	}
	if _, err := run("claude", args...); err != nil {
		return "", fmt.Errorf("claude plugin install: %w", err)
	}
	return "mod: installed from " + modMarketplace + "; it draws the pole above the prompt in sessions started from now on", nil
}

// UninstallMod removes the mod's marketplace, which uninstalls the mod and
// deletes its option. A missing claude command or marketplace means the mod is
// not installed.
func (Adapter) UninstallMod(run client.Runner, dryRun bool) (string, error) {
	args := []string{"plugin", "marketplace", "remove", modName, "--json"}
	if dryRun {
		return "mod: would run claude " + strings.Join(args[:len(args)-1], " "), nil
	}
	out, err := run("claude", args...)
	result, ok := lastJSONLine(out)
	switch {
	case ok && result.Outcome == "ok":
		return "mod: removed", nil
	case ok && result.FailureCode == "not_configured":
		return "mod: not installed", nil
	case errors.Is(err, exec.ErrNotFound):
		return "mod: not installed (claude not found)", nil
	case ok:
		return "", fmt.Errorf("claude plugin marketplace remove: %s", result.Message)
	case err != nil:
		return "", fmt.Errorf("claude plugin marketplace remove: %w", err)
	}
	return "", fmt.Errorf("claude plugin marketplace remove: unexpected output %q", strings.TrimSpace(string(out)))
}

// cliResult is the line a claude plugin command prints with --json.
type cliResult struct {
	Outcome     string `json:"outcome"`
	FailureCode string `json:"failureCode"`
	Message     string `json:"message"`
}

// lastJSONLine reads the result from the last line of out.
func lastJSONLine(out []byte) (cliResult, bool) {
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	var r cliResult
	if json.Unmarshal(lines[len(lines)-1], &r) != nil || r.Outcome == "" {
		return cliResult{}, false
	}
	return r, true
}

// parseVersion reads the version that starts `claude --version`'s output, as
// in "2.1.296 (Claude Code)".
func parseVersion(s string) ([3]int, bool) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return [3]int{}, false
	}
	parts := strings.Split(fields[0], ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

func compareVersions(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return 0
}

func formatVersion(v [3]int) string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}
