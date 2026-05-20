//go:build darwin

package fswatcher

import (
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"
)

// Stream-level event flags.
const (
	fseMustScanSubs  = 0x00000001
	fseUserDropped   = 0x00000002
	fseKernelDropped = 0x00000004
	fseRootChanged   = 0x00000020
)

// File-level event flags (require kFSEventStreamCreateFlagFileEvents).
const (
	fseItemCreated      = 0x00000100
	fseItemRemoved      = 0x00000200
	fseItemInodeMetaMod = 0x00000400
	fseItemRenamed      = 0x00000800
	fseItemModified     = 0x00001000
	fseItemChangeOwner  = 0x00004000
	fseItemXattrMod     = 0x00008000
)

const fseDroppedEvents = fseMustScanSubs | fseUserDropped | fseKernelDropped

type rawFSEvent struct {
	path  string
	flags uint32
}

type translatedFSEvent struct {
	event       Event
	err         error
	rootChanged bool
	rootKey     string
}

type fseventsOverflowError struct {
	Path  string
	Flags uint32
}

func (e fseventsOverflowError) Error() string {
	if e.Path == "" {
		return "fswatcher: events may have been dropped"
	}
	return fmt.Sprintf("fswatcher: events may have been dropped for %s", e.Path)
}

func goString(cStr unsafe.Pointer) string {
	if cStr == nil {
		return ""
	}

	n := 0
	for *(*byte)(unsafe.Add(cStr, uintptr(n))) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(cStr), n))
}

func parseRawFSEvents(n int, pathsPtr, flagsPtr unsafe.Pointer) []rawFSEvent {
	if n <= 0 || pathsPtr == nil || flagsPtr == nil {
		return nil
	}

	events := make([]rawFSEvent, 0, n)
	paths := unsafe.Slice((*unsafe.Pointer)(pathsPtr), n)
	flags := unsafe.Slice((*uint32)(flagsPtr), n)
	for i := 0; i < n; i++ {
		cStr := paths[i]
		flags := flags[i]
		events = append(events, rawFSEvent{
			path:  goString(cStr),
			flags: flags,
		})
	}
	return events
}

func translateFSEventBatch(raw []rawFSEvent, registry *darwinRegistry) []translatedFSEvent {
	if len(raw) == 0 {
		return nil
	}
	if registry == nil {
		return nil
	}

	regs := registry.snapshot()

	out := make([]translatedFSEvent, 0, len(raw))
	for _, ev := range raw {
		p := ev.path
		if abs, err := canonicalize(p); err == nil {
			p = abs
		}

		if ev.flags&fseDroppedEvents != 0 {
			out = append(out, translatedFSEvent{
				err: fseventsOverflowError{Path: p, Flags: ev.flags},
			})
		}

		r, ok := registry.match(p, regs)
		if !ok {
			continue
		}

		// RootChanged events always target the watched root itself. Surface
		// the lifecycle action before any depth filtering, then translate the
		// public event with the explicit root rename/remove fallback.
		if ev.flags&fseRootChanged != 0 {
			action := translatedFSEvent{
				rootChanged: true,
				rootKey:     r.key,
			}
			op := fseventFlagsToOp(ev.flags) & r.op
			if op == 0 {
				op = (Rename | Remove) & r.op
			}
			if op != 0 {
				action.event = Event{Name: r.path, Op: op}
			}
			out = append(out, action)
			continue
		}

		// Suppress events for the watched root directory — its metadata
		// changes are noise. File watches must not be suppressed.
		if r.isDir && p == r.path {
			continue
		}
		if !r.recursive {
			rel, err := filepath.Rel(r.path, p)
			if err != nil || strings.ContainsRune(rel, filepath.Separator) {
				continue
			}
		}

		op := fseventFlagsToOp(ev.flags) & r.op
		if op == 0 {
			continue
		}
		out = append(out, translatedFSEvent{
			event: Event{Name: p, Op: op},
		})
	}
	return out
}

func fseventFlagsToOp(flags uint32) Op {
	var op Op

	if flags&fseItemCreated != 0 {
		op |= Create
	}
	if flags&fseItemRemoved != 0 {
		op |= Remove
	}
	if flags&fseItemRenamed != 0 {
		op |= Rename
	}
	if flags&fseItemModified != 0 {
		op |= Write
	}
	if flags&(fseItemInodeMetaMod|fseItemChangeOwner|fseItemXattrMod) != 0 {
		op |= Chmod
	}

	return op
}
