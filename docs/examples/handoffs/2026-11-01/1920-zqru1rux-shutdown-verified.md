Follows: 2026-11-01/1915-zqru1rux-store-implemented.md
Session: <session ID>

# urlshort: the shutdown path, verified and fixed

## Role for this work

This session was started with `/jalon-resume` on the handoff above. No other
skill or command defines the role. No background task is running; nothing
needs restarting.

Standing rules the user set for the session: English only; work only on this
urlshort project and read nothing outside its directory.

## Goal

Test the two claims the previous handoff carried but never checked: that a real
SIGINT cannot be delivered in this environment, and that `store.close()` is
tidiness rather than correctness. Fix the code if a finding calls for it, run
the suite, no commit.

## What was found, and the decisions

### 1. The inherited SIGINT pitfall is wrong

`1915-zqru1rux-store-implemented.md` listed under "Pitfalls": *"Do not expect
`kill -INT` to reach a backgrounded server in this environment; SIGINT is
ignored for background children of a non-interactive shell."* False.

A background `python3` prints `signal.getsignal(SIGINT)` as
`<built-in function default_int_handler>`, not `SIG_IGN`, and `kill -INT`
raises a real `KeyboardInterrupt`. `setsid` was tried too and is not needed.
CPython installs its own SIGINT handler at startup, which overrides whatever a
non-interactive bash set. The pitfall must be struck from the ledger, not
repeated.

### 2. `store.close()` was silently destructive, and is now loud

- Durability: `close()` buys nothing. A child doing `put()` then `os._exit(9)`
  (no close, no `finally`, no interpreter cleanup) still leaves the complete
  JSON line on disk. The per-`put` flush is what makes data safe. The previous
  handoff was right on this point.
- The real defect: `close()` set `self._file = None`, and `put()` skips the
  write when `_file is None`. So a `put()` after `close()` returned a valid
  short code to the client and never wrote it. No exception, measured.
- The window is reachable: `ThreadingHTTPServer.daemon_threads` is `True`, so
  `server_close()` does not join handler threads. A traced shutdown showed
  `server_close() done; live handler threads=1`, then `store.close()` closing
  the file under that live thread. The window is microseconds (until the
  interpreter kills the daemon threads) and needs a request mid-`put`, but the
  failure mode was a handed-out code silently lost.

Decision: `close()` closes the handle and **keeps** it, so a late `put()` raises
`ValueError: I/O operation on closed file` instead of dropping the link. One
line deleted, one comment added. `close()` stays idempotent (double close of a
file object is allowed) and stays a no-op for an in-memory store.

Rejected: `daemon_threads = False` so `server_close()` joins handlers. With
HTTP/1.1 keep-alive, one idle client would then hang shutdown forever. Not
worth it for a microsecond window.

### 3. The ordering in `__main__.py` is right

`httpd.server_close()` before `store.close()` is correct: the listener must
stop first. Swapping them would widen the window from microseconds to the whole
shutdown. It is not a complete fix, for the daemon-threads reason above, but it
is the right order.

### 4. A leak surfaced by the new test

`Store._load` opened the file without closing it. The new test made the
`ResourceWarning` appear in the suite output, so it was fixed in the same pass:
the read is now under `with`. Two lines.

## Items the previous handoff left open

- The `KeyboardInterrupt` branch of `__main__.py` and `store.close()` in the
  `finally:`: **done**, verified for real here, see Current state.
- Lock contention under real concurrent load: **not carried**, still open. Only
  per-write cost was ever measured, never throughput. Untouched this session.
- File locking if two processes share one `links.db`: **not carried**, still
  open. A single process is still assumed.

## Current state

- Repository: `/path/to/scratchpad/urlshort`.
  A scratchpad path that may not exist in a later session; confirm it before
  acting, and read nothing outside it.
- Read from the repository now: branch `main`, base commit `bb29545`, main
  worktree only. Working tree **dirty and uncommitted**: modified `README.md`,
  `shortener/__main__.py`, `shortener/server.py`, `shortener/store.py`,
  `tests/test_server.py`; untracked `tests/test_store.py`. (`store.py` is new
  but was already untracked at the previous handoff; it still is.) Nothing
  staged, nothing committed. No `links.db` in the tree.
- Python here is 3.14.4. Use `python3`; `python` does not exist.
- Checked: `python3 -m unittest discover -s tests -t .` from the repository
  root, after the fix: **20 tests OK**. Only the two pre-existing
  `HTTPError` `ResourceWarning` lines remain; the `_load` one is gone.
- Checked: real SIGINT to `python3 -m shortener`. Started on a free port,
  POSTed a URL, `kill -INT <pid>`: exit status 0, `links.db` complete.
  Restarted on the same port: `GET /1` answered `302` with the stored URL, the
  next code was `2`, `/stats/1` read 1 after one follow (hits restart at zero,
  as designed).
- Checked: the `finally:` really runs, via a wrapper that monkeypatched
  `Store.close` and `ThreadingHTTPServer.server_close` and called
  `shortener.__main__.main` directly. Order observed: `server_close` called,
  `server_close` done with 1 live handler thread, then `store.close`.
- Checked: `put()` after `close()` raised nothing before the fix; data survives
  `os._exit(9)` without any close; `close()` does release the fd.
- Not checked: the shutdown race was never reproduced end to end as a lost
  link. The window is argued from the trace (live handler after
  `server_close`) plus the measured `put`-after-`close` behaviour.
- Not checked: anything about concurrency or multi-process access.
- Not done: the `README.md` "Storage" section still describes `close()` only as
  closing the handle, and says nothing about the late-`put` behaviour.

## The user's latest request and the answer this reply gave

The user rejected "I cannot deliver a Ctrl-C here" as an end state and asked for
both inherited claims to be attacked, with the code fixed if a finding called
for it, the suite run, and no commit. The reply reported the SIGINT pitfall as
wrong with the probe that disproves it, the verified Ctrl-C run and restart, the
silent-loss finding in `close()` with the one-line fix and why the loud failure
is preferred, the verdict that the `server_close`/`store.close` order is right
and why `daemon_threads = False` was rejected, the 20-test run, and the dirty
uncommitted tree.

## Files involved

- `shortener/store.py` - `close()` keeps the closed handle plus a two-line
  why-comment; `_load` reads under `with`. Everything else as the previous
  handoff describes it.
- `tests/test_store.py` - 6 tests now; the new one is
  `test_a_put_after_close_fails_loudly`.
- `shortener/__main__.py` - unchanged this session; the ordering in its
  `finally:` was judged correct.
- `shortener/server.py`, `tests/test_server.py`, `README.md` - unchanged this
  session.
- Scratchpad used for the signal experiments:
  `/path/to/session-scratchpad/<session ID>/scratchpad/sigtest`,
  holding `probe.py`, `trace.py`, `close_check.py` and their logs. Session
  specific, will not exist later; the findings above are the record, and each
  probe is a few lines to rewrite if ever needed.
- Ports 8097, 8098, 8099 were used for the smoke tests and released. Nothing
  listens on them now; any free port does.

## Next steps, to propose to the user

1. Decide whether to commit. The work is one coherent change across six files
   and the suite is green. The user said no commit this session, so this is a
   question, not a task.
2. Consider two lines in the `README.md` "Storage" section saying that the file
   handle is closed on shutdown and that a write racing shutdown fails rather
   than being silently dropped. Small, and only if the user wants it.
3. Only if the user asks: the concurrency and multi-process questions above.
4. Nothing leaves this machine. No push, tag, pull or merge request has been
   asked for or authorised; any of those would need the user's OK given in the
   new conversation.

## Pitfalls and what not to redo

Verified on `main`, base commit `bb29545`, dirty tree as described, in this
session unless stated.

- **The previous handoff's SIGINT pitfall is withdrawn.** `kill -INT` does
  reach a backgrounded `python3` here; `setsid` is unnecessary. Do not repeat
  the old claim and do not avoid signal testing because of it.
- **Do not use `pkill -f` with a pattern matching `shortener` from a tool
  shell**: the pattern matches the shell's own command line and kills it.
  Inherited and still holding. Record the PID and `kill` it instead.
- **Do not set `daemon_threads = False`** to make `server_close()` join handler
  threads: HTTP/1.1 keep-alive means an idle client hangs shutdown forever.
- **Do not restore `self._file = None` in `close()`.** That is exactly the
  silent-loss bug; a test now fails if it comes back.
- **Do not re-run the base62 ordering experiment.** Inherited and still
  holding: string order and base62 order disagree, so `_load` must use
  `max(self._links, key=decode)`, locked in by the `"z"`/`"A"`/`"B"` test.
- **Do not add a second lock and do not move the append out of the lock.**
  Inherited; nothing this session changed the shape it was measured on.
- **Do not add fsync** on the strength of the figure measured two sessions ago;
  that filesystem is not representative. Still holds.
- **Do not re-argue the rejected storage options** (a class inside `server.py`,
  globals plus `load`/`save`, an abstract base with two subclasses, an env var,
  `sqlite3`). Each cost is recorded in `1908-zqru1rux-storage-layer-decision.md`.
- **Do not persist hit counts** without the user asking; that is the redirect
  hot path, and the `shortcut:` comment says so.
- Use `python3`, not `python`. No third-party imports anywhere.
