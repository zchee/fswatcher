# Design

## Origin

This project is independent of `fsnotify/fsnotify`; its source must
not be consulted while building this one. The public API may end up
looking similar simply because the problem shape is similar.

## Core Watcher

`NewWatcher` returns a `*Watcher`. A caller registers a path together
with the set of `Op` bits it cares about. Change notifications are
delivered on a buffered `Events` channel; non-fatal errors flow on
`Errors`. Both channels are closed when the read loop exits.

The watcher is thread-safe: `Add`, `AddRecursive`, `Remove`, and
`Close` may be called concurrently. Event ordering is preserved as
far as the underlying OS allows.

## Add vs. AddRecursive

Recursive watching is exposed as a dedicated method, not as an option
or variadic argument on `Add`:

- `Add(path, op)` registers exactly one path.
- `AddRecursive(path, op)` registers `path` and every directory
  below it.

The split is intentional. Recursion is bug-prone (subdirectory
lifecycle, walk-vs-event races, fd budgets), so opting in is an
explicit choice the call site makes.

## Remove scope

`Remove` only succeeds on a path that was passed to `Add` or
`AddRecursive`. Sub-watches that `AddRecursive` adds on the user's
behalf cannot be removed independently — calling `Remove` on a
descendant returns `ErrNotAdded`. `Remove` on a recursive root tears
the entire subtree down at once.

## Recursive directory lifecycle

For `AddRecursive`, the watcher is responsible for tracking the
shape of the tree as it changes:

- New subdirectories created inside a recursive root are picked up
  automatically and watched. The walk also descends into the new
  directory in case it appeared with pre-existing children (e.g. a
  rename of an existing tree into the watched root).
- Removed subdirectories are dropped automatically — Linux and macOS
  rely on the kernel's deletion notification, Windows relies on
  `bWatchSubtree`.

## Path normalization

Every path passed to `Add`, `AddRecursive`, or `Remove` is run through
the same canonicalization pipeline so two spellings of the same path
dedupe and `Event.Name` is stable:

- `filepath.Abs` + `filepath.Clean` — relative paths and `.` / `..`
  components collapse.
- `filepath.EvalSymlinks` when the target exists — two paths that
  reach the same target through different symlinks dedupe.
- Windows: `GetLongPathName` to expand 8.3 short forms
  (`C:\PROGRA~1` → `C:\Program Files`), plus a lowercase fold for
  map keys so case-insensitive NTFS comparisons work.
- macOS: comparison keys follow the watched volume's identity policy.
  Case-sensitive volumes keep distinct keys; case-insensitive volumes
  fold consistently, including Unicode normalization when the volume
  reports that behavior. If the policy cannot be read, the Darwin
  backend falls back to conservative case-sensitive keys.

## Testing

Integration tests use real file system events — no mocks. The CI
suite runs on Linux, macOS, Windows, and FreeBSD (under a VM) with
`-race` always enabled; if a backend gets flaky under `-race`, the
timeout grows rather than `-race` getting dropped.

## Platform support

| OS      | Backend                         | Status    |
|---------|---------------------------------|-----------|
| Linux   | inotify                         | Supported |
| Windows | ReadDirectoryChangesW           | Supported |
| macOS   | FSEvents (purego)               | Supported |
| FreeBSD | kqueue                          | Supported |
| other   | stub returning `ErrUnsupported` | —         |

### macOS backend

On macOS the backend is FSEvents, called through
[`purego`](https://github.com/ebitengine/purego) so cgo is not
required. FSEvents monitors paths at the volume level without
opening a file descriptor per watched entry, and supports native
recursive watching — `AddRecursive` creates a single stream
regardless of tree depth.

The Darwin backend is split by ownership boundary:

- `watcher_darwin.go` owns the exported `Watcher` API, channel
  lifecycle, `Add` / `AddRecursive` / `Remove` / `Close`, and the
  read loop that serializes delivery to public channels.
- `fsevents_darwin.go` owns the purego symbol bindings plus checked
  wrappers for CoreFoundation, dispatch, and FSEventStream calls.
- `stream_darwin.go` owns one native `FSEventStreamRef` per user
  registration. A stream is stopped, invalidated, and released exactly
  once.
- `registry_darwin.go` owns watcher callback IDs, registered path
  keys, overlap policy, callback snapshots, and root-removal cleanup.
- `events_darwin.go` owns raw FSEvents callback parsing, event-flag
  translation to public `Op` bits, root-changed handling, and
  dropped-event reporting.
- `path_darwin.go` owns Darwin path identity. It detects the watched
  volume's case and Unicode-normalization behavior when possible and
  falls back conservatively if the volume policy cannot be read.

The first redesign phase intentionally keeps the current stream
topology — one FSEventStream per public registration — because it is
lower risk than changing stream grouping and architecture at the same
time. The registry boundary gives the backend a future place to add a
trie, path-prefix index, consolidated per-watcher stream, or per-volume
stream grouping if benchmarks show the linear callback matching cost is
material.

#### Darwin native resource ownership

Native resource ownership is explicit:

- CoreFoundation strings and arrays created while building a stream are
  released on both success and failure paths after the stream has taken
  the data it needs.
- Failed FSEventStream creation or start releases every native object
  acquired before returning an error to `Add` or `AddRecursive`.
- `Close` snapshots registered streams under the watcher lock, marks
  the watcher closed, stops each native stream, waits for asynchronous
  root-change cleanup, waits for the read loop to close public
  channels, releases the dispatch queue, and unregisters the callback
  ID.
- Callback context passed to FSEvents contains a stable watcher ID
  rather than a Go pointer; callbacks resolve the ID through the global
  registry and ignore events after the watcher has closed.

#### Darwin event semantics

Darwin callback semantics remain normalized before they reach callers.
Non-recursive directory registrations suppress descendant events below
one level; file registrations still report the watched file itself.
Root change notifications remove the affected registration and emit
`Remove` and/or `Rename` when those bits were requested. FSEvents
`MustScanSubDirs` notifications are surfaced on `Errors` as a
non-fatal overflow/rescan signal so callers can reconcile state.
