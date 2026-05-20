//go:build darwin

package fswatcher

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// fseCallback is the C trampoline passed to FSEventStreamCreate.
var fseCallback = purego.NewCallback(func(_ uintptr, clientInfo uintptr, numEvents uintptr, eventPaths unsafe.Pointer, eventFlags unsafe.Pointer, _ uintptr) {
	handleFSEventsCallback(clientInfo, int(numEvents), eventPaths, eventFlags)
})
