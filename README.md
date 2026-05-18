# fswatcher

[![CI](https://github.com/fswatcher/fswatcher/actions/workflows/ci.yml/badge.svg)](https://github.com/fswatcher/fswatcher/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/fswatcher/fswatcher.svg)](https://pkg.go.dev/github.com/fswatcher/fswatcher)

Cross-platform file system notifications for Go.

> Previously published as `github.com/gofsnotify/fsnotify` (package `fsnotify`). The old path is deprecated and redirects here; update imports and rename `fsnotify.X` to `fswatcher.X`. See [#27](https://github.com/fswatcher/fswatcher/issues/27).

## Install

```
go get github.com/fswatcher/fswatcher
```

## Usage

```go
package main

import (
	"log"

	"github.com/fswatcher/fswatcher"
)

func main() {
	w, err := fswatcher.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()

	if err := w.Add("/path/to/dir", fswatcher.Create|fswatcher.Write|fswatcher.Remove); err != nil {
		log.Fatal(err)
	}

	for {
		select {
		case ev := <-w.Events:
			log.Println(ev)
		case err := <-w.Errors:
			log.Println("error:", err)
		}
	}
}
```

## API

- `NewWatcher() (*Watcher, error)` — creates a watcher.
- `(*Watcher).Add(path string, op Op) error` — registers `path` with the given event mask. Returns `ErrAlreadyAdded` if `path` is already registered or is already covered by a recursive registration.
- `(*Watcher).AddRecursive(path string, op Op) error` — registers `path` and every directory under it. New subdirectories created inside are watched automatically; removed subdirectories are dropped. A recursive registration owns its subtree: overlapping descendant registrations, and recursive ancestors that would take over an existing registration, return `ErrAlreadyAdded`. `Remove` may only be called on the original recursive root.
- `(*Watcher).Remove(path string) error` — unregisters `path`. For an `AddRecursive` registration this drops the entire subtree.
- `(*Watcher).Close() error` — stops the watcher and closes the channels.
- `(*Watcher).Events <-chan Event` — receives change notifications.
- `(*Watcher).Errors <-chan error` — receives non-fatal errors.

Paths are canonicalized before registration and removal: they are made
absolute, cleaned, and symlinks are resolved when the target exists. Equality
then follows the platform storage rules used by the backend. Windows expands
8.3 short forms and folds case for path keys. macOS evaluates the containing
volume before choosing its path key policy, so equality is per-volume rather
than OS-wide: default case-insensitive volumes dedupe case and
Unicode-normalization variants, while case-sensitive volumes preserve distinct
names. If macOS volume capability detection fails, fswatcher uses the
conservative case-sensitive policy rather than collapsing paths that may be
distinct. `Event.Name` is returned in canonical form.

## Events

| Op     | Description                                 |
|--------|---------------------------------------------|
| Create | A file or directory was created.            |
| Write  | A file's contents were modified.            |
| Remove | A file or directory was removed.            |
| Rename | A file or directory was renamed or moved.   |
| Chmod  | Permissions or attributes changed.          |

`Op` is a bitmask; combine values with `|` when calling `Add`. The `All` constant is shorthand for the union of every Op bit.

## Rename semantics

`Rename` means the backend observed a path being renamed or moved. The
portable contract is conservative: the source path is reported with
`Rename`, and the destination path is reported with `Create` only when
`Create` was requested and the backend can identify the destination with
sufficient confidence.

On macOS, FSEvents may surface rename paths as one or more file events in
the same callback batch. The Darwin backend may normalize a source and
destination pair only when both paths are present in that same batch and the
pairing is reliable; otherwise it preserves the raw FSEvents rename path(s)
instead of dropping a legitimate path just to force a uniform shape.

Recursive ownership and Darwin rename pairing are part of the public watcher
contract, so the test suite locks their behavior rather than treating them as
backend-only implementation details.

The contract is pinned by integration tests rather than by docs alone:
`watcher_test.go` covers canonicalization, duplicate detection, recursive
ownership, and rename batch expectations, while `path_darwin_test.go` and
`watcher_darwin_test.go` pin the Darwin volume-policy and normalized lookup
rules.

## Guarantees

- Thread-safe: methods may be called from multiple goroutines.
- Event ordering is preserved as far as the underlying OS allows.
- Registration, removal, path identity, channel lifecycle, and error contracts
  are normalized at the public API boundary across supported platforms. Exact event coalescing, duplicate
  notifications, and rename detail remain bounded by the underlying OS backend.
  On macOS, FSEvents rename pairing is only normalized when both paths are
  available in the same callback batch; otherwise fswatcher preserves the raw
  FSEvents rename path(s) instead of guessing and potentially dropping a real
  event.

## Platform Support

| OS      | Backend                 | Status    |
|---------|-------------------------|-----------|
| Linux   | inotify                 | Supported |
| Windows | ReadDirectoryChangesW   | Supported |
| macOS   | FSEvents (purego)       | Supported |
| FreeBSD | kqueue                  | Supported |

## License

MIT
