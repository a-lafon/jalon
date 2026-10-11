# Configuration

Jalon reads `~/.jalon/config.yaml`, or the file given with `-config <path>`. Every field is optional, and unknown fields are rejected, so typos fail at startup. Without a config file, Jalon uses the defaults. Start from [`jalon.example.yaml`](../jalon.example.yaml):

```yaml
context:
  enabled: true
  advise_tokens: 200000        # growth above the starting size at which a reset is first advised
  advise_step_tokens: 150000   # growth needed after an advice before the next one
  stale_after_turns: 5         # tool results older than this many user turns count as stale
```

`enabled: false` silences the hook, and the status line then shows the size without `reset advised`. All values must be positive. `stale_after_turns` only affects the [impact report](impact-report.md).

## jalon init

```sh
jalon init                      # add the prompt hook and the status line
jalon init --dry-run            # show what would change, write nothing
jalon init --statusline=false   # add the prompt hook alone
jalon init --mod                # add the prompt hook, and the mod in place of the status line
jalon init --uninstall          # remove what Jalon added, the mod included
jalon init --advise-tokens 250000 --advise-step-tokens 100000   # also set the thresholds
```

`jalon init` writes an absolute path, so the hook works even where your shell's `PATH` is not set, as in the VS Code extension: the `jalon` found on your `PATH` when it is the binary you run, so that an upgrade that replaces the file behind a link, as Homebrew does, keeps the hook working; otherwise the path of the binary you run. It adds its entry after your other hooks, keeps every other setting and their order, and writes nothing when nothing changes. Before writing, it keeps the previous file as `settings.json.jalon.bak`. Running it again from another binary, after moving or reinstalling Jalon, updates the entry in place. `--uninstall` removes only Jalon's entries, never `~/.jalon` or your handoffs. Changes apply to sessions started afterwards.

The status line is added only when you have none; see [status line](how-it-works.md#status-line) to add Jalon's to yours. `--mod` installs the [mod](how-it-works.md#the-pole-above-the-prompt) with Claude Code's own command, `claude plugin install jalon --marketplace a-lafon/jalon`, which downloads it from GitHub, and sets its `binary` option to the path the hook records. It removes Jalon's status line, since the mod draws the same pole. It needs Claude Code 2.1.292 or later, and refuses an older one before writing anything. Run it again after moving or reinstalling Jalon to update the path. `--uninstall` removes the mod with `claude plugin marketplace remove jalon`, which also deletes its option.

It then prints the advisor's thresholds in force and where they come from, the config file or the defaults:

```
Thresholds in force: advise_tokens 200,000, advise_step_tokens 150,000, from the defaults (no config file).
Change them with jalon init --advise-tokens <n> --advise-step-tokens <n>.
```

`--advise-tokens` and `--advise-step-tokens` set those values in the `context` section of `~/.jalon/config.yaml`, and create the file when there is none. Only those lines change: the other keys, the comments and the layout stay as written. The result is checked as Jalon checks its config at startup, and an invalid value, such as `--advise-tokens 0`, writes neither the config nor the settings. As with the settings, nothing is written when nothing changes, the previous file is kept as `config.yaml.jalon.bak`, and `--dry-run` prints the resulting `context` section. `--uninstall` refuses these flags.

## Installing by hand

Add to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [{ "type": "command", "command": "/absolute/path/to/jalon hook prompt --client claude-code", "timeout": 2 }] }
    ]
  }
}
```

## Files

Everything Jalon records stays in `~/.jalon/`:

- `sessions/`: the context advisor's state, one small JSON file per session, removed after 7 days.
- `config.yaml`: your configuration.

Handoff files are the exception: they live in your projects, under `.jalon/handoffs/` (see [handoffs](handoffs.md#where-they-go)). A session started in your home directory, outside a git repository, keeps them in `~/.jalon/handoffs/`.
