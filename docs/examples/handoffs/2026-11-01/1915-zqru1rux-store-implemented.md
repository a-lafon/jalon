Follows: 2026-11-01/1908-zqru1rux-storage-layer-decision.md
Session: 7dd7a04d-7a0b-4124-bb2e-e23ed72a5c3b

# urlshort: the storage layer, implemented

## Role for this work

This session was started with `/jalon-resume` on the handoff above. No other
skill or command defines the role, and no background task is running or needs
restarting.

## Goal

Implement the storage decision taken in the previous session: move the link
table out of `shortener/server.py` into a `Store` class, make file backing one
constructor argument, and give the file-backed path the test it did not have.
The user asked for the whole plan for real, with the test suite run after each
piece rather than only at the end, and asked to be told at once if anything in
the code, the tests or the standard library contradicted the decision.

## What was done

All six steps of the previous handoff's "Next steps" are done. Nothing was
committed; the user said not to.

1. `shortener/store.py` written to the agreed signature list.
2. `shortener/server.py` lost `_LINKS`, `_HITS`, `_COUNTER`, `_LOCK`, `reset`,
   `put`, `get`, `hits` and the `itertools`/`threading` imports. `validate` and
   the whole HTTP surface are untouched. `make_server(port=0, store=None)` sets
   `httpd.store = store or Store()`; `Handler` calls `self.server.store.put/
   get/hits`. The module docstring's "Open question" paragraph became one line
   pointing at `.store`.
3. `shortener/__main__.py` gained three lines: the `Store` import,
   `store = Store("links.db")` passed to `make_server`, and `store.close()` in
   the existing `finally:`.
4. `tests/test_server.py` got exactly the six-point diff: the `Store` import,
   `setUp` building a fresh `Store()` on `self.httpd.store` with the comment
   rewritten, and `TestCounterIsGlobal.test_first_code_after_reset_is_stable`
   renamed to `TestCounterStartsAtOne.test_first_code_is_one` with its comment
   deleted. `setUpClass` unchanged, no assertion changed.
5. `tests/test_store.py` created, 5 tests (below).
6. `README.md`: the "Open question" section replaced by a "Storage" section;
   the `What is here` list now names `shortener/store.py` and no longer says
   `server.py` holds the link table.

## Decisions taken in this session, beyond the inherited ones

- `_load` skips a line that is valid JSON but missing `"code"` or `"url"`
  through the same `except (ValueError, KeyError, TypeError)` as an undecodable
  one. The previous handoff listed this as not checked; it now has a test.
- `_load` returns silently on `FileNotFoundError`, so a first run with no
  `links.db` is normal rather than an error.
- Blank lines are skipped without counting as corruption.
- The append handle is opened after `_load`, so the file is read before it is
  opened for writing.
- The two `shortcut:` comments are in `store.py`: one in `__init__` (hits are
  not persisted, reset to 0, upgrade when durable counters are asked for), one
  at the flush in `put` (flush but no fsync; adding fsync means moving the write
  out of the lock and re-measuring on the target disk first).

## Items the previous handoff left open

- Loader behaviour on a line missing `"code"` or `"url"`: **done**, implemented
  and tested (`test_a_line_missing_a_key_is_skipped`).
- Lock contention under real concurrent load: **not carried**, still open. Only
  the per-write cost was ever measured, never throughput. Nothing in this
  session touched it.
- File locking if two processes share one `links.db`: **not carried**, still
  open. A single process is still assumed, and two servers on one file would
  interleave appends.

## Current state

- Repository: `/path/to/scratchpad/urlshort`.
  A scratchpad path that may not exist in a later session; confirm it before
  acting, and read nothing outside it.
- Read from the repository now: branch `main`, base commit `bb29545`, no
  worktree other than the main one. Working tree **dirty and uncommitted**:
  modified `README.md`, `shortener/__main__.py`, `shortener/server.py`,
  `tests/test_server.py`; untracked `shortener/store.py`, `tests/test_store.py`.
  Nothing staged, nothing committed this session.
- Checked: `python3 -m unittest discover -s tests -t .` from the repository
  root, after each piece. 14 tests OK after `store.py`; 9 errors
  (`module 'shortener.server' has no attribute 'reset'`) in the intended red
  window between the `server.py` change and the test diff; 14 OK once the test
  diff landed; **19 tests OK** after `test_store.py`, and 19 OK again after the
  README rewrite. The two pre-existing `ResourceWarning` lines about `HTTPError`
  cleanup still appear and are still noise.
- Checked: the real run across a restart. Started `python3 -m shortener 8099`,
  shortened one URL, killed it, restarted on the same port: `GET /1` answered
  `302 https://example.com/real`, the next code was `2`, and `links.db` held
  both JSON lines. `/stats/1` read 1 after one follow, confirming hits restart
  at zero. Port 8099 was chosen for the test only; nothing listens on it now.
  The `links.db` this produced was deleted, so the tree carries no data file.
- **Not checked**: the `KeyboardInterrupt` branch of `__main__.py`, hence
  `store.close()` in the `finally:`. A non-interactive shell makes background
  children ignore SIGINT, so no real Ctrl-C could be delivered here; the smoke
  test used SIGTERM, which skips `finally` entirely. Data was intact anyway
  because `put` flushes per link, but those three lines have never run.
- Not checked: anything about concurrency or multi-process access, as above.

## The user's latest request and the answer this reply gave

The user asked for the full implementation, a test run after each piece, and an
immediate stop-and-report if anything contradicted the decision. The reply gave
the per-piece table of runs including the intentional 9-error window, described
the five new tests, reported the restart smoke test, and stated plainly that
nothing contradicted the decision. It named the Ctrl-C path as the one thing it
could not verify and handed over the two-line command to confirm it by hand. It
listed the dirty tree and confirmed nothing was committed.

## Files involved

- `shortener/store.py` - new, the whole storage layer. ~80 lines.
- `shortener/server.py` - HTTP surface only now.
- `shortener/__main__.py` - three added lines.
- `tests/test_server.py` - the six-point diff, 14 tests.
- `tests/test_store.py` - new, 5 tests: links survive a reconstruction and the
  counter continues; a file holding codes `"z"` (35) then `"A"` (36) resumes at
  `"B"` (37); a truncated last line is skipped with `skipped 1` on stderr while
  the good line loads; a line missing `"url"` takes the same skip path;
  `Store()` writes no file.
- `README.md` - "Open question" replaced by "Storage".
- `shortener/codec.py`, `tests/test_codec.py` - untouched. `decode` is now
  called in anger by `Store._load`.
- `.gitignore` - already lists `/links.db`; unchanged.

## Next steps, to propose to the user

1. Confirm the Ctrl-C path by hand in a real terminal, from the repository root:
   `python3 -m shortener 8080`, shorten something, Ctrl-C, check the process
   exits cleanly and `links.db` is complete. Then delete the `links.db` it
   creates, since it is written into the launch directory.
2. Decide whether to commit. The work is one coherent change across six files;
   the user has not asked for a commit and explicitly said not to make one this
   session, so this is a question, not a task.
3. Only if the user asks: the concurrency and multi-process questions above.
4. Nothing leaves this machine. No push, tag, pull or merge request has been
   asked for or authorised, and none may be done without the user saying so in
   the new conversation.

## Pitfalls and what not to redo

Verified on `main`, base commit `bb29545`, dirty tree as described, in this
session unless stated.

- **Do not re-run the base62 ordering experiment.** Inherited and still holding:
  string order and base62 order disagree, so `_load` must use
  `max(self._links, key=decode)`. `tests/test_store.py` now locks this in with
  the `"z"`/`"A"`/`"B"` case, so a regression to text comparison fails a test
  rather than silently duplicating codes.
- **Do not add a second lock and do not move the append out of the lock.**
  Inherited; nothing in this session's implementation changed the shape it was
  measured on. The write sits inside the single existing lock in `Store.put`.
- **Do not add fsync** on the strength of the figure measured in the previous
  session; that filesystem is not representative. Still holds.
- **Do not re-argue the rejected options** (a class inside `server.py`, globals
  plus `load`/`save`, an abstract base with two subclasses, an env var,
  `sqlite3`). Each one's cost is recorded in the handoff this one follows.
- **Do not let `_load` raise on a bad line.** Now enforced by two tests.
- **Do not persist hit counts** without the user asking; that is the redirect
  hot path, and the `shortcut:` comment says so.
- **Do not use `pkill -f` with a pattern matching `shortener` from a tool
  shell**: the pattern matches the shell's own command line and kills it. Two
  commands died that way here. Record the PID and `kill` it instead.
- **Do not expect `kill -INT` to reach a backgrounded server** in this
  environment; SIGINT is ignored for background children of a non-interactive
  shell, and the command hangs on `wait`. This is why the Ctrl-C path is
  unverified, and it is a shell artefact, not a defect in the code.
- Use `python3`, not `python`.
- No third-party imports; `store.py` uses `itertools`, `json`, `sys`,
  `threading` and `.codec` only.
