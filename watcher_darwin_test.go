//go:build darwin

package fswatcher

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func TestNormalizeDarwinPathFoldsCaseAndNormalization(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		left  string
		right string
	}{
		{
			name:  "case",
			left:  "/Volumes/Work/README",
			right: "/volumes/work/readme",
		},
		{
			name:  "unicode normalization",
			left:  "/Volumes/Work/Café",
			right: "/volumes/work/Café",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotLeft := normalizeDarwinPath(tc.left, false)
			gotRight := normalizeDarwinPath(tc.right, false)
			if gotLeft != gotRight {
				t.Fatalf("normalizeDarwinPath(%q) = %q, normalizeDarwinPath(%q) = %q; want equal", tc.left, gotLeft, tc.right, gotRight)
			}
		})
	}
}

func TestMatchRegistrationPrefersMostSpecificPath(t *testing.T) {
	t.Parallel()

	registry := newDarwinRegistry()
	root := "/Volumes/Work/root"
	nested := filepath.Join(root, "nested")
	regs := []darwinRegistration{
		{key: registry.keyFor(root), path: root, op: Write, recursive: true, isDir: true},
		{key: registry.keyFor(nested), path: nested, op: Create, recursive: false, isDir: true},
	}

	got, ok := registry.match(filepath.Join(nested, "child.txt"), regs)
	if !ok {
		t.Fatal("registry.match returned no match")
	}
	if got.path != nested {
		t.Fatalf("registry.match selected %q, want %q", got.path, nested)
	}
	if got.op != Create {
		t.Fatalf("registry.match op = %s, want %s", got.op, Create)
	}
}

func TestCreateStreamLockedCleansUpOnStartFailure(t *testing.T) {
	restore := replaceDarwinNativeFuncsForTest(t)

	const (
		cfPath    = uintptr(11)
		pathArray = uintptr(22)
		stream    = uintptr(33)
		queue     = uintptr(44)
		watcherID = uintptr(55)
	)

	var releases []uintptr
	var appended, queued, invalidated, releasedStream bool
	_cfStringCreateWithCString = func(_ uintptr, cStr string, encoding uint32) uintptr {
		if cStr != "/tmp/root" {
			t.Fatalf("CFStringCreateWithCString path = %q, want /tmp/root", cStr)
		}
		if encoding != kCFStringEncodingUTF8 {
			t.Fatalf("CFStringCreateWithCString encoding = %#x, want %#x", encoding, kCFStringEncodingUTF8)
		}
		return cfPath
	}
	_cfArrayCreateMutable = func(_ uintptr, capacity int64, _ uintptr) uintptr {
		if capacity != 1 {
			t.Fatalf("CFArrayCreateMutable capacity = %d, want 1", capacity)
		}
		return pathArray
	}
	_cfArrayAppendValue = func(arr, value uintptr) {
		if arr != pathArray || value != cfPath {
			t.Fatalf("CFArrayAppendValue(%d, %d), want (%d, %d)", arr, value, pathArray, cfPath)
		}
		appended = true
	}
	_cfRelease = func(ref uintptr) {
		releases = append(releases, ref)
	}
	_fseStreamCreate = func(_ uintptr, callback uintptr, ctx *fsEventStreamContext, paths uintptr, sinceWhen uint64, latency float64, flags uint32) uintptr {
		if callback != fseCallback {
			t.Fatalf("FSEventStreamCreate callback = %d, want %d", callback, fseCallback)
		}
		if ctx == nil || ctx.Info != watcherID {
			t.Fatalf("FSEventStreamCreate ctx.Info = %v, want %d", ctx, watcherID)
		}
		if paths != pathArray {
			t.Fatalf("FSEventStreamCreate paths = %d, want %d", paths, pathArray)
		}
		if sinceWhen != kFSEventStreamEventIdSinceNow {
			t.Fatalf("FSEventStreamCreate sinceWhen = %d, want %d", sinceWhen, uint64(kFSEventStreamEventIdSinceNow))
		}
		if latency != defaultLatency {
			t.Fatalf("FSEventStreamCreate latency = %f, want %f", latency, defaultLatency)
		}
		wantFlags := uint32(fseCreateFileEvents | fseCreateNoDefer | fseCreateWatchRoot)
		if flags != wantFlags {
			t.Fatalf("FSEventStreamCreate flags = %#x, want %#x", flags, wantFlags)
		}
		return stream
	}
	_fseStreamSetDispatchQueue = func(gotStream, gotQueue uintptr) {
		if gotStream != stream || gotQueue != queue {
			t.Fatalf("FSEventStreamSetDispatchQueue(%d, %d), want (%d, %d)", gotStream, gotQueue, stream, queue)
		}
		queued = true
	}
	_fseStreamStart = func(gotStream uintptr) uintptr {
		if gotStream != stream {
			t.Fatalf("FSEventStreamStart(%d), want %d", gotStream, stream)
		}
		return 0
	}
	_fseStreamInvalidate = func(gotStream uintptr) {
		if gotStream != stream {
			t.Fatalf("FSEventStreamInvalidate(%d), want %d", gotStream, stream)
		}
		invalidated = true
	}
	_fseStreamRelease = func(gotStream uintptr) {
		if gotStream != stream {
			t.Fatalf("FSEventStreamRelease(%d), want %d", gotStream, stream)
		}
		releasedStream = true
	}

	w := &Watcher{id: watcherID, queue: queue}
	got, err := w.createStreamLocked("/tmp/root", "root-key", Write, false, true)
	if got != nil {
		t.Fatalf("createStreamLocked returned stream %+v after start failure", got)
	}
	if err == nil || !strings.Contains(err.Error(), "FSEventStreamStart failed") {
		t.Fatalf("createStreamLocked error = %v, want FSEventStreamStart failed", err)
	}
	if !appended {
		t.Fatal("CFArrayAppendValue was not called")
	}
	if !queued {
		t.Fatal("FSEventStreamSetDispatchQueue was not called")
	}
	if !invalidated {
		t.Fatal("FSEventStreamInvalidate was not called after start failure")
	}
	if !releasedStream {
		t.Fatal("FSEventStreamRelease was not called after start failure")
	}
	if want := []uintptr{cfPath, pathArray}; !slices.Equal(releases, want) {
		t.Fatalf("CF releases = %v, want %v", releases, want)
	}

	restore()
}

func TestCreateStreamLockedReleasesCFStringOnPathArrayFailure(t *testing.T) {
	restore := replaceDarwinNativeFuncsForTest(t)

	const cfPath = uintptr(11)
	var releases []uintptr
	_cfStringCreateWithCString = func(uintptr, string, uint32) uintptr {
		return cfPath
	}
	_cfArrayCreateMutable = func(uintptr, int64, uintptr) uintptr {
		return 0
	}
	_cfRelease = func(ref uintptr) {
		releases = append(releases, ref)
	}

	w := &Watcher{}
	got, err := w.createStreamLocked("/tmp/root", "root-key", Write, false, true)
	if got != nil {
		t.Fatalf("createStreamLocked returned stream %+v after path array failure", got)
	}
	if err == nil || !strings.Contains(err.Error(), "CFArrayCreateMutable failed") {
		t.Fatalf("createStreamLocked error = %v, want CFArrayCreateMutable failed", err)
	}
	if want := []uintptr{cfPath}; !slices.Equal(releases, want) {
		t.Fatalf("CF releases = %v, want %v", releases, want)
	}

	restore()
}

func TestDarwinStreamStopIsConcurrentAndIdempotent(t *testing.T) {
	restore := replaceDarwinNativeFuncsForTest(t)

	const stream = uintptr(33)
	var stops, invalidates, releases, badHandle atomic.Int64
	_fseStreamStop = func(got uintptr) {
		if got != stream {
			badHandle.Add(1)
		}
		stops.Add(1)
	}
	_fseStreamInvalidate = func(got uintptr) {
		if got != stream {
			badHandle.Add(1)
		}
		invalidates.Add(1)
	}
	_fseStreamRelease = func(got uintptr) {
		if got != stream {
			badHandle.Add(1)
		}
		releases.Add(1)
	}

	s := &darwinStream{stream: stream}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(s.stop)
	}
	wg.Wait()
	s.stop()

	if got := badHandle.Load(); got != 0 {
		t.Fatalf("native release functions saw %d unexpected stream handles", got)
	}
	if got := stops.Load(); got != 1 {
		t.Fatalf("FSEventStreamStop calls = %d, want 1", got)
	}
	if got := invalidates.Load(); got != 1 {
		t.Fatalf("FSEventStreamInvalidate calls = %d, want 1", got)
	}
	if got := releases.Load(); got != 1 {
		t.Fatalf("FSEventStreamRelease calls = %d, want 1", got)
	}
	if s.stream != 0 {
		t.Fatalf("stream handle after stop = %d, want 0", s.stream)
	}
	if !s.stopped {
		t.Fatal("stream stopped flag is false after stop")
	}

	restore()
}

func TestHandleFSEventsRootChangedFallbacksToRequestedOps(t *testing.T) {
	oldStop, oldInvalidate, oldRelease := _fseStreamStop, _fseStreamInvalidate, _fseStreamRelease
	_fseStreamStop = func(uintptr) {}
	_fseStreamInvalidate = func(uintptr) {}
	_fseStreamRelease = func(uintptr) {}
	t.Cleanup(func() {
		_fseStreamStop = oldStop
		_fseStreamInvalidate = oldInvalidate
		_fseStreamRelease = oldRelease
	})

	cases := []struct {
		name string
		op   Op
		want Op
	}{
		{name: "rename", op: Rename, want: Rename},
		{name: "remove", op: Remove, want: Remove},
		{name: "all", op: All, want: Rename | Remove},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &Watcher{
				internalEv:  make(chan Event, 1),
				internalErr: make(chan error, 1),
				registry:    newDarwinRegistry(),
				done:        make(chan struct{}),
				exited:      make(chan struct{}),
			}
			w.id = registerWatcher(w)
			t.Cleanup(func() {
				unregisterWatcher(w.id)
			})

			root := filepath.Join(t.TempDir(), "watched")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatalf("Mkdir: %v", err)
			}
			absRoot, err := canonicalize(root)
			if err != nil {
				t.Fatalf("canonicalize(%q): %v", root, err)
			}
			rootKey := w.registry.keyFor(absRoot)

			if err := w.registry.add(&darwinStream{
				stream:    1,
				key:       rootKey,
				path:      absRoot,
				op:        tc.op,
				recursive: false,
				isDir:     true,
			}); err != nil {
				t.Fatalf("registry.add: %v", err)
			}
			if got, ok := w.registry.match(absRoot, w.registry.snapshot()); !ok {
				t.Fatal("registry.match returned no match before root-changed callback")
			} else if got.path != absRoot {
				t.Fatalf("registry.match path = %q, want %q", got.path, absRoot)
			}

			cstr := append([]byte(absRoot), 0)
			paths := []unsafe.Pointer{unsafe.Pointer(&cstr[0])}
			flags := []uint32{fseRootChanged}
			if got := lookupWatcher(w.id); got == nil {
				t.Fatal("lookupWatcher returned nil before callback")
			}
			handleFSEventsCallback(
				w.id,
				1,
				unsafe.Pointer(&paths[0]),
				unsafe.Pointer(&flags[0]),
			)
			w.cleanupW.Wait()

			regs := w.registry.snapshot()
			stillTracked := false
			for _, reg := range regs {
				if reg.key == rootKey {
					stillTracked = true
					break
				}
			}
			if stillTracked {
				t.Fatalf("root stream still tracked after root-changed callback")
			}

			select {
			case ev := <-w.internalEv:
				if ev.Name != absRoot {
					t.Fatalf("event name = %q, want %q", ev.Name, absRoot)
				}
				if ev.Op != tc.want {
					t.Fatalf("event op = %s, want %s", ev.Op, tc.want)
				}
			case err := <-w.internalErr:
				t.Fatalf("unexpected error: %v", err)
			case <-time.After(eventTimeout):
				t.Fatalf("timeout waiting for root-changed event")
			}
		})
	}
}

func TestFSEventFlagsToOp(t *testing.T) {
	t.Parallel()

	got := fseventFlagsToOp(fseItemCreated | fseItemModified | fseItemInodeMetaMod | fseItemRenamed | fseItemRemoved | fseItemChangeOwner | fseItemXattrMod)
	want := Create | Write | Remove | Rename | Chmod
	if got != want {
		t.Fatalf("fseventFlagsToOp(...) = %s, want %s", got, want)
	}
}

func TestGoString(t *testing.T) {
	t.Parallel()

	buf := []byte("hello\x00ignored")
	got := goString(unsafe.Pointer(&buf[0]))
	if got != "hello" {
		t.Fatalf("goString(...) = %q, want %q", got, "hello")
	}
}

func replaceDarwinNativeFuncsForTest(t *testing.T) func() {
	t.Helper()

	oldCFStringCreateWithCString := _cfStringCreateWithCString
	oldCFArrayCreateMutable := _cfArrayCreateMutable
	oldCFArrayAppendValue := _cfArrayAppendValue
	oldCFRelease := _cfRelease
	oldFSEStreamCreate := _fseStreamCreate
	oldFSEStreamSetDispatchQueue := _fseStreamSetDispatchQueue
	oldFSEStreamStart := _fseStreamStart
	oldFSEStreamStop := _fseStreamStop
	oldFSEStreamInvalidate := _fseStreamInvalidate
	oldFSEStreamRelease := _fseStreamRelease

	restore := func() {
		_cfStringCreateWithCString = oldCFStringCreateWithCString
		_cfArrayCreateMutable = oldCFArrayCreateMutable
		_cfArrayAppendValue = oldCFArrayAppendValue
		_cfRelease = oldCFRelease
		_fseStreamCreate = oldFSEStreamCreate
		_fseStreamSetDispatchQueue = oldFSEStreamSetDispatchQueue
		_fseStreamStart = oldFSEStreamStart
		_fseStreamStop = oldFSEStreamStop
		_fseStreamInvalidate = oldFSEStreamInvalidate
		_fseStreamRelease = oldFSEStreamRelease
	}
	t.Cleanup(restore)
	return restore
}
