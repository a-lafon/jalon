Session: <session ID>

# urlshort: the storage layer decision

## Goal

Answer the README's "Open question" (README.md lines 27-41): where the storage
layer lives, and how it is swapped between in-memory for tests and file-backed
for real runs, without an abstraction larger than the thing it stores.

The user asked for a decision, explicitly **no code this turn**, then asked for
three checks against the code. Both are done. Nothing has been implemented.

## The decision

**Storage lives in a new `shortener/store.py`: one class `Store`, no base class,
no second class, no factory.** It owns what the module globals own today
(`_LINKS`, `_HITS`, `_COUNTER`, `_LOCK` in `shortener/server.py:24-27`) as
instance attributes. `reset()` disappears; constructing a `Store()` is the reset.

Memory vs file is one optional constructor argument, not a second type:

- `Store()` - in memory, what tests use.
- `Store("links.db")` - the same, plus append-on-put and load-on-start.
  `/links.db` is already in `.gitignore`, so that filename was the intended one.

`put` appends one JSON line `{"code": ..., "url": ...}` and flushes when a path
was given. `__init__` reads the file back and restores the counter past
`max(decode(code))`. That finally uses `codec.decode`, which today is tested but
called by nothing.

**Hits are not persisted**: they reset to 0 on restart, with a `shortcut:`
comment. Persisting them means a disk write on every redirect, which is the hot
path, and nobody asked for durable counters.

Swapping is done by passing the store to the server and letting the handler read
it off the server instance:

```
def make_server(port=0, store=None):
    httpd = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    httpd.store = store or Store()
    return httpd
```

`Handler` calls `self.server.store.put/get/hits`. `server.py` loses `_LINKS`,
`_HITS`, `_COUNTER`, `_LOCK`, `reset`, `put`, `get`, `hits` and the
`itertools`/`threading` imports; `validate` and all HTTP code stay. No
module-level mutable state remains anywhere.

`shortener/__main__.py` changes by three lines: import `Store`, pass
`Store("links.db")` to `make_server`, and call `store.close()` in the existing
`finally:` block (so the append handle closes on Ctrl-C rather than at
interpreter teardown).

Standard library only throughout: `json`, `threading`, `os`, `sys`, `.codec`.

## Final shape agreed for store.py (signatures only, no bodies written)

```
class Store:
    def __init__(self, path=None)   # path, _file, _links, _hits, _counter, _lock
    def put(self, url)              # -> code; appends+flushes under the lock when path is set
    def get(self, code)             # -> url or None; counts the hit
    def hits(self, code)            # -> int or None
    def close(self)                 # closes the append handle; no-op in memory
    def _load(self)                 # reads path, skips undecodable lines, sets _counter past max(decode)
```

## The three checks, and what they changed

### 1. Counter restore - the `decode` is load-bearing

Base62 ordering and integer ordering **do not agree**. `shortener/codec.py:3`
orders digits, lowercase, uppercase; ASCII puts uppercase (65-90) before
lowercase (97-122). Verified by running `encode` over 1..4999 in this repository:
**84 adjacent pairs** sort lexicographically backwards (`z`->`A`, `Z`->`10`,
`1z`->`1A`, ...). After 4999 links, a string `max` gives `'zz'` = 2205 while the
real high-water mark is `'1iD'` = 4998, which would hand out roughly 2800
duplicate codes and silently overwrite live links.

So `max(codes, key=decode)` is required, and any later "optimisation" to compare
codes as text is a data-loss bug. A cheaper variant exists (codes are written in
issue order, so the last line is the maximum) but was rejected: it breaks on a
hand-edited or reordered file for no real saving, since a few thousand decodes at
startup cost nothing.

**Undecodable code in the file**: `decode` raises
`ValueError("bad character ... in code")` (`codec.py:29-30`), which would escape
`Store.__init__` and stop the service booting until someone edits the file by
hand. A truncated tail line after a crash is exactly that case. Decided: `_load`
**skips** a line it cannot parse or decode, counts them, prints one line to
stderr naming the count. It does not skip silently and does not repair the file.

### 2. Hot path - one lock, flush inside it, no fsync

`ThreadingHTTPServer` is one thread per connection, so every redirect takes the
same lock a file-backed `put` would hold across write+flush. Measured in this
environment: append + flush of one line is about **3.6 us**, an order of
magnitude below the HTTP handling already in the path, and `put` runs once per
shorten while the lock is otherwise taken by redirects.

Conclusion: the single existing lock is enough, the append goes **inside** it,
and no second lock is added. Keeping the write under the lock also serialises
appends for free, so no reliance on `O_APPEND` atomicity.

The fsync figure measured alongside it (about 5.2 us) is **not trustworthy** -
that filesystem is almost certainly tmpfs or an overlay, where a real disk is
0.1-10 ms. Hence: no fsync. If durability against power loss is ever required,
that is the moment the append must move out of the lock, and it must be
re-measured on the target disk first. This belongs in the `shortcut:` comment.

### 3. The test diff is four edits in three places, not one line

An earlier claim of "one line" was wrong. Against `tests/test_server.py` as it
stands:

- line 8: add `from shortener.store import Store`.
- lines 13-17 `setUpClass`: **unchanged**. `make_server(0)` still works because
  `store=None` builds an in-memory `Store`, and `setUp` replaces it before the
  first request.
- lines 26-27 `setUp`: `server.reset()` becomes `self.httpd.store = Store()`,
  comment rewritten. This is the only place reaching into the server module's
  state.
- line 99: `TestCounterIsGlobal` -> `TestCounterStartsAtOne`.
- line 100: `test_first_code_after_reset_is_stable` -> `test_first_code_is_one`.
- line 101: the comment "Passes only because setUp reset the module-level
  counter" is deleted; the assertion becomes a property of a fresh `Store`.

`server.make_server` (lines 14-15) is the only other `server.` reference and
survives. No test body or assertion changes. `tests/test_codec.py` untouched.

## Options rejected, and what each loses

- **A class inside `server.py`**: identical test and `__main__` diff, so it costs
  nothing less; it loses the separation the module's own docstring asks for, and
  the next person adding an endpoint reads storage code first.
- **Keep globals, add `load()`/`save()`**: `reset()` survives under a new name,
  tests still reach into another module's globals, and one global table makes it
  impossible to run two stores side by side, so the file-backed path cannot be
  tested without writing a real file and clobbering the in-memory tests.
- **Abstract `Storage` base with `MemoryStore`/`FileStore`**: three classes for a
  dict, both subclasses sharing `get`/`hits` verbatim; the inheritance only
  expresses a choice already expressible as `path=None`, and it forces a factory
  or an `if` somewhere that `make_server(port, store)` does not need.
- **Env var (`SHORTENER_DB`) picking the backend**: hidden global config; tests
  must set and unset it or risk a developer shell writing test links into a real
  file, and isolation still has to come from a passed store anyway.
- **`sqlite3`**: stdlib so the no-dependency rule holds, and it would give durable
  hits free, but it brings connection-per-thread handling, schema creation and SQL
  into a project whose storage is two dicts and a counter, and makes the in-memory
  variant a second code path (`:memory:` plus setup) rather than "skip the file".
  Revisit only if hit counts must survive a restart; the `Store` surface would not
  change.

## Current state

- Repository: `/path/to/scratchpad/urlshort`.
  This is a scratchpad path and may not exist in a later session; confirm it
  before acting, and read nothing outside it.
- Branch `main`, base commit `bb29545` ("write the README and the open storage
  question"), working tree **clean**, nothing staged, no new files. Read from the
  repository at handoff time, not copied.
- Checked: `python3 -m unittest discover -s tests -t .` from the repository root
  runs 14 tests, OK. Two `ResourceWarning` lines about `HTTPError` cleanup appear
  and are pre-existing noise, not failures. Note `python` does not exist in this
  environment; use `python3`.
- Checked: the base62 ordering finding above, by running `encode`/`decode` over
  1..4999 through a `python3 -c` one-liner. No file was added to the repository.
- Checked: the append+flush timing above, in a throwaway one-liner outside the
  repository. The fsync half of it is explicitly not trusted.
- Not checked: loader behaviour on a line whose JSON is valid but missing the
  `"code"` or `"url"` key (same skip path, untested); lock contention under real
  concurrent load, since only the write cost was measured, not throughput; any
  need for file locking if two server processes share one `links.db` (a single
  process is assumed).
- Not written: `shortener/store.py` and every edit listed above. All of it is a
  decision on paper.

## The user's latest request and what this reply gave

The user asked for the three checks to be run against the code rather than
against reasoning, then for the final `store.py` as a signature list only. The
reply reported: the ordering disagreement with its 84-pair measurement and the
duplicate-code consequence, the decision to skip undecodable lines, the
single-lock conclusion with the timing and the fsync caveat, the corrected
six-point test diff, and the signature list. It also stated plainly that the
earlier "one line" test-diff claim was wrong.

## Files involved

- `README.md` - the "Open question" at lines 27-41 is the task; it will want
  rewriting once the decision lands.
- `shortener/server.py` - loses the storage half, keeps the HTTP surface and
  `validate`.
- `shortener/codec.py` - unchanged; its `decode` becomes used in anger.
- `shortener/__main__.py` - three lines.
- `tests/test_server.py` - the six-point diff above.
- `tests/test_codec.py` - unchanged.
- `.gitignore` - already lists `/links.db`.
- `shortener/store.py` - to be created.

## Next steps, to confirm with the user

1. Implement `Store` exactly to the signature list, with the two `shortcut:`
   comments (hits not persisted; no fsync, and what would have to change if it
   were added).
2. Apply the `server.py` removal, the `make_server(port, store=None)` signature,
   the handler's `self.server.store` calls, and the three `__main__.py` lines.
3. Apply the six-point test diff and re-run
   `python3 -m unittest discover -s tests -t .` from the repository root,
   expecting 14 tests OK.
4. Add one small test for the file-backed path: a `Store(tmpfile)` that survives
   being reconstructed, with the counter continuing rather than restarting, and a
   deliberately corrupt last line being skipped. This is the new non-trivial
   logic and is currently the only part with no check.
5. Rewrite the README's "Open question" into a short description of what was
   decided.
6. Nothing leaves this machine without the user saying so.

## Pitfalls and what not to redo

All verified on `main` at `bb29545`, clean tree.

- **Do not re-run the base62 ordering experiment.** It is settled: string order
  and integer order disagree, 84 inversions in the first 4999 codes, and sorting
  codes as text would duplicate roughly 2800 of them. Use `max(..., key=decode)`.
- **Do not add a second lock** for the file append, and do not move the write
  outside the lock. Measured at about 3.6 us, inside the lock, no fsync.
- **Do not add fsync** on the strength of the 5.2 us figure measured here; that
  filesystem is not representative.
- **Do not re-argue the rejected options** (class in `server.py`, globals plus
  `load`/`save`, abstract base with two subclasses, env var, sqlite3). Each one's
  cost is recorded above.
- **Do not claim the test diff is one line.** It is four edits across lines 8,
  26-27 and 99-101; `setUpClass` stays as it is.
- **Do not let `_load` raise on a bad line.** A truncated tail after a crash must
  not stop the service booting.
- **Do not persist hit counts** without the user asking; that is the redirect hot
  path.
- Use `python3`, not `python`, in this environment.
- No third-party imports anywhere; stdlib only, as the README requires.
