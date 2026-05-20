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
- `(*Watcher).Add(path string, op Op) error` — registers `path` with the given event mask. Returns `ErrAlreadyAdded` if `path` is already registered.
- `(*Watcher).AddRecursive(path string, op Op) error` — registers `path` and every directory under it. New subdirectories created inside are watched automatically; removed subdirectories are dropped. `Remove` may only be called on the original recursive root.
- `(*Watcher).Remove(path string) error` — unregisters `path`. For an `AddRecursive` registration this drops the entire subtree.
- `(*Watcher).Close() error` — stops the watcher and closes the channels.
- `(*Watcher).Events <-chan Event` — receives change notifications.
- `(*Watcher).Errors <-chan error` — receives non-fatal errors.

Paths are canonicalized (absolute, cleaned, with symlinks resolved when the
target exists; on Windows 8.3 short forms are expanded and case is folded), so
two spellings of the same path dedupe and `Event.Name` is always returned in
canonical form. On macOS, registration keys follow the watched volume's
path-identity policy instead of assuming every volume has the same case or
Unicode-normalization behavior.

## Events

| Op     | Description                                 |
|--------|---------------------------------------------|
| Create | A file or directory was created.            |
| Write  | A file's contents were modified.            |
| Remove | A file or directory was removed.            |
| Rename | A file or directory was renamed or moved.   |
| Chmod  | Permissions or attributes changed.          |

`Op` is a bitmask; combine values with `|` when calling `Add`. The `All` constant is shorthand for the union of every Op bit.

## Guarantees

- Thread-safe: methods may be called from multiple goroutines.
- Event ordering is preserved as far as the underlying OS allows.
- Behavior is normalized across supported platforms.

## Platform Support

| OS      | Backend                 | Status    |
|---------|-------------------------|-----------|
| Linux   | inotify                 | Supported |
| Windows | ReadDirectoryChangesW   | Supported |
| macOS   | FSEvents (purego)       | Supported |
| FreeBSD | kqueue                  | Supported |

### macOS notes

The macOS backend uses FSEvents through
[`purego`](https://github.com/ebitengine/purego), so applications do not need
cgo to use it. Recursive watches use native FSEvents recursion instead of
opening one file descriptor per descendant, while non-recursive directory
watches filter child events before they reach `Events`.

If macOS reports that events may have been dropped, the watcher sends a
non-fatal error on `Errors`; callers should rescan or reconcile the affected
tree. When a watched root is removed or renamed, the watcher drops that
registration and emits the requested `Remove` and/or `Rename` event bits when
the platform reports enough information to do so.

Path matching on macOS follows the watched volume's identity policy: case and
Unicode-normalization folding are used on case-insensitive volumes, and the
backend falls back to conservative case-sensitive matching when the volume
policy cannot be determined.
If a recursive root and a more-specific registration both cover an event path,
the more-specific registration handles the event so each watch's operation mask
remains meaningful.

## License

MIT
