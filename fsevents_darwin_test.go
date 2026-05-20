//go:build darwin

package fswatcher

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestRegisterNativeFuncWrapsPanic(t *testing.T) {
	var fn func()

	err := registerNativeFunc(&fn, 0, "missing_symbol")
	if err == nil {
		t.Fatal("registerNativeFunc returned nil error for missing symbol")
	}
	if got := err.Error(); !strings.Contains(got, "lookup missing_symbol") {
		t.Fatalf("registerNativeFunc error = %q, want missing_symbol context", got)
	}
}

func TestNativeAllocationHelpersAndCleanupPaths(t *testing.T) {
	oldStringCreate := _cfStringCreateWithCString
	oldArrayCreate := _cfArrayCreateMutable
	oldArrayAppend := _cfArrayAppendValue
	oldRelease := _cfRelease
	t.Cleanup(func() {
		_cfStringCreateWithCString = oldStringCreate
		_cfArrayCreateMutable = oldArrayCreate
		_cfArrayAppendValue = oldArrayAppend
		_cfRelease = oldRelease
	})

	var released []uintptr
	var appended []uintptr
	_cfRelease = func(ref uintptr) {
		released = append(released, ref)
	}

	_cfStringCreateWithCString = func(uintptr, string, uint32) uintptr {
		return 0
	}
	if _, err := newCFString("missing"); err == nil {
		t.Fatal("newCFString returned nil error when creation failed")
	}

	_cfStringCreateWithCString = func(uintptr, string, uint32) uintptr {
		return 11
	}
	_cfArrayCreateMutable = func(uintptr, int64, uintptr) uintptr {
		return 0
	}
	released = released[:0]
	if _, cleanup, err := newCFPathArray("/tmp/example"); err == nil {
		t.Fatal("newCFPathArray returned nil error when array creation failed")
	} else if cleanup != nil {
		t.Fatal("newCFPathArray returned cleanup for failed allocation")
	}
	if len(released) != 1 || released[0] != 11 {
		t.Fatalf("newCFPathArray failure released = %v, want [11]", released)
	}

	_cfArrayCreateMutable = func(uintptr, int64, uintptr) uintptr {
		return 22
	}
	_cfArrayAppendValue = func(arr uintptr, value uintptr) {
		appended = append(appended, arr, value)
	}
	released = released[:0]
	appended = appended[:0]
	arr, cleanup, err := newCFPathArray("/tmp/example")
	if err != nil {
		t.Fatalf("newCFPathArray returned error: %v", err)
	}
	if arr != 22 {
		t.Fatalf("newCFPathArray returned array handle %d, want 22", arr)
	}
	if cleanup == nil {
		t.Fatal("newCFPathArray returned nil cleanup on success")
	}
	cleanup()
	if len(appended) != 2 || appended[0] != 22 || appended[1] != 11 {
		t.Fatalf("newCFPathArray append trace = %v, want [22 11]", appended)
	}
	if len(released) != 2 || released[0] != 11 || released[1] != 22 {
		t.Fatalf("newCFPathArray success/release trace = %v, want [11 22]", released)
	}
}

func TestNativeStreamHelpersValidateInputsAndReleaseOwnership(t *testing.T) {
	oldQueueCreate := _dispatchQueueCreate
	oldQueueRelease := _dispatchRelease
	oldCFRelease := _cfRelease
	oldStreamCreate := _fseStreamCreate
	oldSetQueue := _fseStreamSetDispatchQueue
	oldStart := _fseStreamStart
	oldStop := _fseStreamStop
	oldInvalidate := _fseStreamInvalidate
	oldStreamRelease := _fseStreamRelease
	t.Cleanup(func() {
		_dispatchQueueCreate = oldQueueCreate
		_dispatchRelease = oldQueueRelease
		_cfRelease = oldCFRelease
		_fseStreamCreate = oldStreamCreate
		_fseStreamSetDispatchQueue = oldSetQueue
		_fseStreamStart = oldStart
		_fseStreamStop = oldStop
		_fseStreamInvalidate = oldInvalidate
		_fseStreamRelease = oldStreamRelease
	})

	var setQueueCalls []uintptr
	var started []uintptr
	var stopped []uintptr
	var invalidated []uintptr
	var released []uintptr
	_cfRelease = func(ref uintptr) {
		released = append(released, ref)
	}
	_dispatchRelease = func(ref uintptr) {
		released = append(released, ref)
	}

	_dispatchQueueCreate = func(string, uintptr) uintptr { return 0 }
	if _, err := newDispatchQueue("queue"); err == nil {
		t.Fatal("newDispatchQueue returned nil error when queue creation failed")
	}

	_dispatchQueueCreate = func(string, uintptr) uintptr { return 33 }
	q, err := newDispatchQueue("queue")
	if err != nil {
		t.Fatalf("newDispatchQueue returned error: %v", err)
	}
	if q != 33 {
		t.Fatalf("newDispatchQueue returned queue handle %d, want 33", q)
	}

	if _, err := createFSEventStream(1, 0, 2); err == nil {
		t.Fatal("createFSEventStream accepted zero path array")
	}
	if _, err := createFSEventStream(1, 2, 0); err == nil {
		t.Fatal("createFSEventStream accepted zero callback")
	}

	var gotCtx *fsEventStreamContext
	var gotSince uint64
	var gotLatency float64
	var gotFlags uint32
	var gotCallback uintptr
	var gotPathArray uintptr
	_fseStreamCreate = func(alloc uintptr, callback uintptr, ctx *fsEventStreamContext, pathArray uintptr, sinceWhen uint64, latency float64, flags uint32) uintptr {
		gotCallback = callback
		gotCtx = ctx
		gotPathArray = pathArray
		gotSince = sinceWhen
		gotLatency = latency
		gotFlags = flags
		return 44
	}
	stream, err := createFSEventStream(99, 88, 77)
	if err != nil {
		t.Fatalf("createFSEventStream returned error: %v", err)
	}
	if stream != 44 {
		t.Fatalf("createFSEventStream returned stream handle %d, want 44", stream)
	}
	if gotCallback != 77 || gotPathArray != 88 || gotSince != kFSEventStreamEventIdSinceNow || gotLatency != defaultLatency {
		t.Fatalf("createFSEventStream captured callback=%d pathArray=%d since=%d latency=%v", gotCallback, gotPathArray, gotSince, gotLatency)
	}
	if gotCtx == nil || gotCtx.Info != 99 {
		t.Fatalf("createFSEventStream context = %+v, want Info=99", gotCtx)
	}
	if gotFlags != uint32(fseCreateFileEvents|fseCreateNoDefer|fseCreateWatchRoot) {
		t.Fatalf("createFSEventStream flags = %#x, want %#x", gotFlags, uint32(fseCreateFileEvents|fseCreateNoDefer|fseCreateWatchRoot))
	}

	_fseStreamCreate = func(uintptr, uintptr, *fsEventStreamContext, uintptr, uint64, float64, uint32) uintptr {
		return 0
	}
	if _, err := createFSEventStream(1, 2, 3); err == nil {
		t.Fatal("createFSEventStream returned nil error when native create failed")
	}

	_fseStreamSetDispatchQueue = func(stream uintptr, queue uintptr) {
		setQueueCalls = append(setQueueCalls, stream, queue)
	}
	_fseStreamStart = func(uintptr) uintptr {
		started = append(started, 55)
		return 0
	}
	_fseStreamInvalidate = func(stream uintptr) {
		invalidated = append(invalidated, stream)
	}
	_fseStreamRelease = func(stream uintptr) {
		released = append(released, stream)
	}
	if err := startFSEventStream(55, 66); err == nil {
		t.Fatal("startFSEventStream returned nil error when start failed")
	}
	if len(setQueueCalls) != 2 || setQueueCalls[0] != 55 || setQueueCalls[1] != 66 {
		t.Fatalf("startFSEventStream set-queue calls = %v, want [55 66]", setQueueCalls)
	}
	if len(started) != 1 || started[0] != 55 {
		t.Fatalf("startFSEventStream start calls = %v, want [55]", started)
	}
	if len(invalidated) != 1 || invalidated[0] != 55 {
		t.Fatalf("startFSEventStream invalidations = %v, want [55]", invalidated)
	}
	if len(released) != 1 || released[0] != 55 {
		t.Fatalf("startFSEventStream releases = %v, want [55]", released)
	}

	setQueueCalls = setQueueCalls[:0]
	started = started[:0]
	stopped = stopped[:0]
	invalidated = invalidated[:0]
	released = released[:0]
	_fseStreamStart = func(uintptr) uintptr { return 1 }
	if err := startFSEventStream(56, 67); err != nil {
		t.Fatalf("startFSEventStream returned unexpected error on success path: %v", err)
	}
	if len(setQueueCalls) != 2 || setQueueCalls[0] != 56 || setQueueCalls[1] != 67 {
		t.Fatalf("startFSEventStream success set-queue calls = %v, want [56 67]", setQueueCalls)
	}
	if len(started) != 0 || len(invalidated) != 0 || len(released) != 0 {
		t.Fatalf("startFSEventStream success should not release, saw start=%v invalidate=%v release=%v", started, invalidated, released)
	}

	released = released[:0]
	releaseCF(0)
	releaseCF(77)
	releaseDispatchQueue(0)
	releaseDispatchQueue(88)
	if len(released) != 2 || released[0] != 77 || released[1] != 88 {
		t.Fatalf("releaseCF/releaseDispatchQueue trace = %v, want [77 88]", released)
	}

	released = released[:0]
	stopped = stopped[:0]
	invalidated = invalidated[:0]
	_fseStreamStop = func(stream uintptr) {
		stopped = append(stopped, stream)
	}
	releaseFSEventStream(0)
	releaseFSEventStream(99)
	if len(stopped) != 1 || stopped[0] != 99 {
		t.Fatalf("releaseFSEventStream stop trace = %v, want [99]", stopped)
	}
	if len(invalidated) != 1 || invalidated[0] != 99 {
		t.Fatalf("releaseFSEventStream invalidations = %v, want [99]", invalidated)
	}
	if len(released) != 1 || released[0] != 99 {
		t.Fatalf("releaseFSEventStream releases = %v, want [99]", released)
	}
}

func TestInitFSEventsCachesFirstFailure(t *testing.T) {
	oldDlopen := _nativeDlopen
	oldDlsym := _nativeDlsym
	oldRegister := _nativeRegisterFunc
	t.Cleanup(func() {
		_nativeDlopen = oldDlopen
		_nativeDlsym = oldDlsym
		_nativeRegisterFunc = oldRegister
		fseInitOnce = sync.Once{}
		fseInitErr = nil
	})

	fseInitOnce = sync.Once{}
	fseInitErr = nil

	calls := 0
	_nativeDlopen = func(string, int) (uintptr, error) {
		calls++
		return 0, errors.New("dlopen boom")
	}
	_nativeDlsym = func(uintptr, string) (uintptr, error) {
		t.Fatal("unexpected dlsym call during dlopen failure")
		return 0, nil
	}
	_nativeRegisterFunc = func(any, uintptr) {
		t.Fatal("unexpected register call during dlopen failure")
	}

	first := initFSEvents()
	second := initFSEvents()
	if first == nil {
		t.Fatal("initFSEvents returned nil error on forced dlopen failure")
	}
	if !strings.Contains(first.Error(), "load CoreFoundation") {
		t.Fatalf("initFSEvents error = %v, want CoreFoundation load failure", first)
	}
	if second == nil || second.Error() != first.Error() {
		t.Fatalf("initFSEvents cached error = %v, want %v", second, first)
	}
	if calls != 1 {
		t.Fatalf("dlopen calls = %d, want 1", calls)
	}
}

func TestDoInitFSEventsReportsMandatoryLookupFailures(t *testing.T) {
	cases := []struct {
		name    string
		failSym string
		want    string
	}{
		{name: "core foundation symbol", failSym: "CFStringCreateWithCString", want: "lookup CFStringCreateWithCString"},
		{name: "dispatch queue create", failSym: "dispatch_queue_create", want: "lookup dispatch_queue_create"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldDlopen := _nativeDlopen
			oldDlsym := _nativeDlsym
			oldRegister := _nativeRegisterFunc
			t.Cleanup(func() {
				_nativeDlopen = oldDlopen
				_nativeDlsym = oldDlsym
				_nativeRegisterFunc = oldRegister
				fseInitOnce = sync.Once{}
				fseInitErr = nil
			})

			fseInitOnce = sync.Once{}
			fseInitErr = nil

			_nativeDlopen = func(string, int) (uintptr, error) {
				return 1, nil
			}
			_nativeDlsym = func(_ uintptr, name string) (uintptr, error) {
				if name == tc.failSym {
					return 0, errors.New("symbol lookup boom")
				}
				if name == "kCFTypeArrayCallBacks" {
					return 1, nil
				}
				return 1, nil
			}
			_nativeRegisterFunc = func(any, uintptr) {}

			if err := doInitFSEvents(); err == nil {
				t.Fatalf("doInitFSEvents returned nil error for %s", tc.failSym)
			} else if got := err.Error(); !strings.Contains(got, tc.want) {
				t.Fatalf("doInitFSEvents error = %q, want substring %q", got, tc.want)
			}
		})
	}
}

func TestDoInitFSEventsReportsCallbackLookupFailure(t *testing.T) {
	oldDlopen := _nativeDlopen
	oldDlsym := _nativeDlsym
	oldRegister := _nativeRegisterFunc
	t.Cleanup(func() {
		_nativeDlopen = oldDlopen
		_nativeDlsym = oldDlsym
		_nativeRegisterFunc = oldRegister
		fseInitOnce = sync.Once{}
		fseInitErr = nil
	})

	fseInitOnce = sync.Once{}
	fseInitErr = nil

	_nativeDlopen = func(string, int) (uintptr, error) {
		return 1, nil
	}
	_nativeDlsym = func(_ uintptr, name string) (uintptr, error) {
		if name == "kCFTypeArrayCallBacks" {
			return 0, errors.New("missing callback table")
		}
		return 1, nil
	}
	_nativeRegisterFunc = func(any, uintptr) {}

	err := doInitFSEvents()
	if err == nil {
		t.Fatal("doInitFSEvents returned nil error on missing callback table")
	}
	if !strings.Contains(err.Error(), "lookup kCFTypeArrayCallBacks") {
		t.Fatalf("doInitFSEvents error = %q, want callback lookup failure", err)
	}
}
