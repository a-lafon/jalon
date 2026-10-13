package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/a-lafon/jalon/internal/cli"
	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/config"
)

// executable is os.Executable, lookPath is exec.LookPath and runClient runs the
// client's own CLI; tests replace them.
var (
	executable = os.Executable
	lookPath   = exec.LookPath
	runClient  = runCommand
)

// runInit runs "jalon init": it installs Jalon's hooks and its status line into
// the client's settings, or with -mod the client's mod in place of the status
// line, or removes them all with -uninstall. After an install it prints the
// advisor's thresholds, which -advise-tokens and -advise-step-tokens set in the
// config file.
func runInit(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	clientName := flags.String("client", defaultClient, "coding agent to install Jalon into")
	statusLine := flags.Bool("statusline", true, "install the status line (terminal only; never replaces another one)")
	mod := flags.Bool("mod", false, "install the client's mod in place of the status line (Claude Code 2.1.292 or later; downloads it from GitHub)")
	uninstall := flags.Bool("uninstall", false, "remove what Jalon installed")
	dryRun := flags.Bool("dry-run", false, "print what would change, write nothing")
	settingsPath := flags.String("settings", "", "client settings file (default: the client's user settings)")
	adviseTokens := flags.Int("advise-tokens", 0, "set context.advise_tokens in the config file")
	adviseStepTokens := flags.Int("advise-step-tokens", 0, "set context.advise_step_tokens in the config file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// Only the threshold flags given are set: 0 is a value to refuse, not "unset".
	var tokens, stepTokens *int
	statusLineSet := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "advise-tokens":
			tokens = adviseTokens
		case "advise-step-tokens":
			stepTokens = adviseStepTokens
		case "statusline":
			statusLineSet = true
		}
	})
	setThresholds := tokens != nil || stepTokens != nil
	if *uninstall && setThresholds {
		return errors.New("-uninstall does not take -advise-tokens or -advise-step-tokens; nothing written")
	}
	if *uninstall && *mod {
		return errors.New("-uninstall removes the mod too, it takes no -mod; nothing written")
	}
	if *mod && statusLineSet && *statusLine {
		return errors.New("-mod draws the pole in place of the status line, drop -statusline; nothing written")
	}
	display := client.NoDisplay
	switch {
	case *mod:
		display = client.Mod
	case *statusLine:
		display = client.StatusLine
	}
	adapter, err := clientFor(*clientName)
	if err != nil {
		return err
	}
	installer, ok := adapter.(client.Installer)
	if !ok {
		return fmt.Errorf("cannot install into %s: add its hook by hand, see \"Installing by hand\" in docs/configuration.md", *clientName)
	}
	modInstaller, hasMod := adapter.(client.ModInstaller)
	if *mod {
		if !hasMod {
			return fmt.Errorf("%s has no mod: use the status line; nothing written", *clientName)
		}
		if err := modInstaller.CheckMod(runClient); err != nil {
			return fmt.Errorf("%w; nothing written", err)
		}
	}
	path := *settingsPath
	if path == "" {
		if path, err = installer.SettingsPath(); err != nil {
			return err
		}
	}

	current, mode, err := readFile(path)
	if err != nil {
		return err
	}
	var next []byte
	var actions []string
	var exe string
	if *uninstall {
		next, actions, err = installer.Uninstall(current)
	} else {
		if exe, err = installableExecutable(); err != nil {
			return err
		}
		next, actions, err = installer.Install(current, exe, display)
	}
	if err != nil {
		return fmt.Errorf("%s: %w; nothing written", path, err)
	}

	// The config file, the one the hook reads, is checked before anything is
	// written, so an invalid threshold writes neither file. -uninstall leaves it
	// alone.
	configPath := config.DefaultPath()
	var cfgCurrent, cfgNext []byte
	var cfgMode fs.FileMode
	var cfg config.Config
	if !*uninstall {
		if cfgCurrent, cfgMode, err = readFile(configPath); err != nil {
			return err
		}
		cfgNext = cfgCurrent
		if setThresholds {
			if cfgNext, err = config.SetThresholds(configPath, cfgCurrent, tokens, stepTokens); err != nil {
				return fmt.Errorf("%w; nothing written", err)
			}
		}
		if cfg, err = config.Parse(configPath, cfgNext); err != nil {
			return fmt.Errorf("%w; nothing written", err)
		}
	}

	for _, a := range actions {
		fmt.Fprintln(out, a)
	}
	wrote, err := replaceFile(out, path, current, next, mode, *dryRun, "It would become:\n"+string(next))
	if err != nil {
		return err
	}
	if wrote {
		fmt.Fprintln(out, "It applies to sessions started from now on.")
	}
	if *uninstall {
		if !hasMod {
			return nil
		}
		done, err := modInstaller.UninstallMod(runClient, *dryRun)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, done)
		return nil
	}
	if *mod {
		done, err := modInstaller.InstallMod(runClient, exe, *dryRun)
		if err != nil {
			return fmt.Errorf("%w; the settings are written, run jalon init -mod again", err)
		}
		fmt.Fprintln(out, done)
	}

	if setThresholds {
		section, err := config.ContextSection(cfgNext)
		if err != nil {
			return err
		}
		if _, err := replaceFile(out, configPath, cfgCurrent, cfgNext, cfgMode, *dryRun, "Its context section would become:\n"+section); err != nil {
			return err
		}
	}
	label, source := "Thresholds in force", "from "+configPath
	if *dryRun && !bytes.Equal(cfgNext, cfgCurrent) {
		label = "Thresholds once written"
	}
	if cfgNext == nil {
		source = "from the defaults (no config file)"
	}
	fmt.Fprintf(out, "%s: advise_tokens %s, advise_step_tokens %s, %s.\n", label,
		cli.Thousands(int64(cfg.Context.AdviseTokens)), cli.Thousands(int64(cfg.Context.AdviseStepTokens)), source)
	fmt.Fprintln(out, "Change them with jalon init --advise-tokens <n> --advise-step-tokens <n>.")
	return nil
}

// replaceFile writes next over the file at path and says what it did. It
// writes nothing when next is the current content, nor with -dry-run, which
// prints preview instead. It reports whether it wrote.
func replaceFile(out io.Writer, path string, current, next []byte, mode fs.FileMode, dryRun bool, preview string) (bool, error) {
	switch {
	case bytes.Equal(next, current):
		fmt.Fprintf(out, "%s: nothing to change.\n", path)
		return false, nil
	case dryRun:
		fmt.Fprintf(out, "Dry run, %s not written. %s", path, preview)
		return false, nil
	}
	backup, err := writeFile(path, current, next, mode)
	if err != nil {
		return false, err
	}
	if backup != "" {
		fmt.Fprintf(out, "Wrote %s (previous version in %s).\n", path, backup)
	} else {
		fmt.Fprintf(out, "Wrote %s.\n", path)
	}
	return true, nil
}

// installableExecutable is the absolute path the hook records: the jalon found
// on the PATH when it is this binary once links are resolved, else the path the
// OS reports for this binary. os.Executable keeps a link on macOS but resolves
// it on Linux, where a package manager's upgrade would delete the versioned
// file it names; the PATH entry is the link the upgrade keeps pointing at the
// new version. A go run build is refused: its path disappears when it exits.
func installableExecutable() (string, error) {
	exe, err := executable()
	if err != nil {
		return "", fmt.Errorf("cannot find the jalon binary: %w", err)
	}
	if strings.Contains(filepath.ToSlash(exe), "/go-build") {
		return "", fmt.Errorf("%s is a temporary go run build: install jalon (go install or a release) and run its init", exe)
	}
	if found, err := lookPath("jalon"); err == nil {
		if abs, err := filepath.Abs(found); err == nil && sameFile(abs, exe) {
			return abs, nil
		}
	}
	return exe, nil
}

// runCommand runs a command and returns its standard output; a failure carries
// what the command printed, its standard error first.
func runCommand(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}

// sameFile tells whether two paths name the same file, links followed.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

// readFile returns a file Jalon edits, the client's settings or the config, and
// its permissions, or nothing and 0600 when it does not exist yet.
func readFile(path string) ([]byte, fs.FileMode, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0o600, nil
	}
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	return b, info.Mode().Perm(), nil
}

// writeFile saves the current file next to it, then replaces it atomically
// with next. A symlinked file, as in a dotfiles repository, has its target
// replaced and the link kept. It returns the backup path, if any.
func writeFile(path string, current, next []byte, mode fs.FileMode) (string, error) {
	target := path
	if current != nil {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", err
		}
		target = resolved
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	backup := ""
	if current != nil {
		backup = target + ".jalon.bak"
		if err := os.WriteFile(backup, current, mode); err != nil {
			return "", fmt.Errorf("backup: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".jalon-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := tmp.Write(next); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	return backup, nil
}
