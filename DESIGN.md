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

A recursive registration owns the subtree rooted at the path the user
registered. A later `Add` or `AddRecursive` for a descendant is a
duplicate and returns `ErrAlreadyAdded`; so does adding a recursive
ancestor that would absorb an existing registration. Non-recursive
sibling and parent/child registrations remain independent when neither
registration is a recursive owner of the other.

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
- macOS: path keys are volume-sensitive. The backend probes the
  containing volume, so equality is per-volume rather than OS-wide. On
  case-insensitive volumes, keys fold case and Unicode-normalization
  variants so duplicate spellings dedupe. On case-sensitive volumes,
  names that differ only by case stay distinct. If volume capability
  detection fails, the backend uses the conservative case-sensitive
  policy rather than collapsing paths that the file system may treat
  as different.

## Rename semantics

`Rename` means the backend observed a path being renamed or moved. The
portable contract is intentionally conservative: the source path is
reported with `Rename` when the backend can identify it; the destination
path is reported as `Create` only when `Create` was requested and the
backend can identify the destination with sufficient confidence. Backends
must not drop a legitimate path merely to force a uniform event shape.

On macOS, FSEvents may expose rename paths as one or more file events in
the same callback batch. The Darwin backend may normalize an in-batch
source/destination pair only when both paths are present in that same
batch and the pairing is reliable; if pairing is not reliable it
preserves the raw FSEvents rename path(s). This keeps the contract
honest while still allowing stronger normalization when the callback
data proves it.

Recursive subtree ownership is also contract-level behavior: a recursive
registration owns its descendants, overlapping registrations are rejected,
and the tests treat that rule as stable API surface rather than a Darwin-only
quirk.

The test anchors for this contract are split by concern: `watcher_test.go`
holds the shared canonicalization, duplicate, recursive-ownership, and
rename-batch cases; `path_darwin_test.go` checks volume-sensitive path-key
policy; and `watcher_darwin_test.go` checks normalized registration lookup
and root-changed dispatch.

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
recursive watching — `AddRecursive` creates a single stream for the
recursive root regardless of tree depth. The backend applies the
public recursive-ownership rules before creating streams so
overlapping registrations cannot mask each other during callback
dispatch.
Rename pairing is best effort: if a callback batch provides a
reliable source/destination pair, the backend may normalize it; if
not, it preserves the raw FSEvents rename path(s) rather than
discarding one path to force a uniform event shape.

Darwin path identity is not OS-global. APFS and HFS+ can be mounted with
different case-sensitivity policies, so the backend probes the relevant
volume and chooses a path-key policy for that volume. Detection failures
fall back to a case-sensitive key. That fallback may allow duplicate
spellings on a case-insensitive volume, but it avoids merging two
distinct paths on a case-sensitive volume.
