//go:build darwin

package fswatcher

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

var (
	_nativeDlopen       = purego.Dlopen
	_nativeDlsym        = purego.Dlsym
	_nativeRegisterFunc = purego.RegisterFunc

	_cfStringCreateWithCString func(alloc uintptr, cStr string, encoding uint32) uintptr
	_cfArrayCreateMutable      func(alloc uintptr, capacity int64, callbacks uintptr) uintptr
	_cfArrayAppendValue        func(arr uintptr, value uintptr)
	_cfRelease                 func(ref uintptr)

	_fseStreamCreate           func(alloc uintptr, callback uintptr, ctx *fsEventStreamContext, paths uintptr, sinceWhen uint64, latency float64, flags uint32) uintptr
	_fseStreamSetDispatchQueue func(stream uintptr, queue uintptr)
	_fseStreamStart            func(stream uintptr) uintptr
	_fseStreamStop             func(stream uintptr)
	_fseStreamInvalidate       func(stream uintptr)
	_fseStreamRelease          func(stream uintptr)

	_dispatchQueueCreate func(label string, attr uintptr) uintptr
	_dispatchRelease     func(queue uintptr)

	cfTypeArrayCallBacks uintptr
)

var (
	fseInitOnce sync.Once
	fseInitErr  error
)

type nativeSymbol struct {
	dst    any
	handle uintptr
	name   string
}

func initFSEvents() error {
	fseInitOnce.Do(func() {
		fseInitErr = doInitFSEvents()
	})
	return fseInitErr
}

func doInitFSEvents() error {
	cf, err := _nativeDlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("fswatcher: load CoreFoundation: %w", err)
	}
	cs, err := _nativeDlopen("/System/Library/Frameworks/CoreServices.framework/CoreServices", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("fswatcher: load CoreServices: %w", err)
	}
	ls, err := _nativeDlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("fswatcher: load libSystem: %w", err)
	}

	var (
		cfStringCreateWithCString func(alloc uintptr, cStr string, encoding uint32) uintptr
		cfArrayCreateMutable      func(alloc uintptr, capacity int64, callbacks uintptr) uintptr
		cfArrayAppendValue        func(arr uintptr, value uintptr)
		cfRelease                 func(ref uintptr)

		fseStreamCreate           func(alloc uintptr, callback uintptr, ctx *fsEventStreamContext, paths uintptr, sinceWhen uint64, latency float64, flags uint32) uintptr
		fseStreamSetDispatchQueue func(stream uintptr, queue uintptr)
		fseStreamStart            func(stream uintptr) uintptr
		fseStreamStop             func(stream uintptr)
		fseStreamInvalidate       func(stream uintptr)
		fseStreamRelease          func(stream uintptr)

		dispatchQueueCreate func(label string, attr uintptr) uintptr
		dispatchRelease     func(queue uintptr)
	)

	symbols := []nativeSymbol{
		{dst: &cfStringCreateWithCString, handle: cf, name: "CFStringCreateWithCString"},
		{dst: &cfArrayCreateMutable, handle: cf, name: "CFArrayCreateMutable"},
		{dst: &cfArrayAppendValue, handle: cf, name: "CFArrayAppendValue"},
		{dst: &cfRelease, handle: cf, name: "CFRelease"},
		{dst: &fseStreamCreate, handle: cs, name: "FSEventStreamCreate"},
		{dst: &fseStreamSetDispatchQueue, handle: cs, name: "FSEventStreamSetDispatchQueue"},
		{dst: &fseStreamStart, handle: cs, name: "FSEventStreamStart"},
		{dst: &fseStreamStop, handle: cs, name: "FSEventStreamStop"},
		{dst: &fseStreamInvalidate, handle: cs, name: "FSEventStreamInvalidate"},
		{dst: &fseStreamRelease, handle: cs, name: "FSEventStreamRelease"},
		{dst: &dispatchQueueCreate, handle: ls, name: "dispatch_queue_create"},
	}
	for _, symbol := range symbols {
		if err := registerNativeFunc(symbol.dst, symbol.handle, symbol.name); err != nil {
			return err
		}
	}
	if err := registerOptionalNativeFunc(&dispatchRelease, ls, "dispatch_release"); err != nil {
		return err
	}

	sym, err := _nativeDlsym(cf, "kCFTypeArrayCallBacks")
	if err != nil {
		return fmt.Errorf("fswatcher: lookup kCFTypeArrayCallBacks: %w", err)
	}
	if sym == 0 {
		return fmt.Errorf("fswatcher: lookup kCFTypeArrayCallBacks: symbol missing")
	}

	_cfStringCreateWithCString = cfStringCreateWithCString
	_cfArrayCreateMutable = cfArrayCreateMutable
	_cfArrayAppendValue = cfArrayAppendValue
	_cfRelease = cfRelease

	_fseStreamCreate = fseStreamCreate
	_fseStreamSetDispatchQueue = fseStreamSetDispatchQueue
	_fseStreamStart = fseStreamStart
	_fseStreamStop = fseStreamStop
	_fseStreamInvalidate = fseStreamInvalidate
	_fseStreamRelease = fseStreamRelease

	_dispatchQueueCreate = dispatchQueueCreate
	_dispatchRelease = dispatchRelease
	cfTypeArrayCallBacks = sym
	return nil
}

func registerNativeFunc(fptr any, handle uintptr, name string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("fswatcher: lookup %s: %v", name, r)
		}
	}()

	sym, err := _nativeDlsym(handle, name)
	if err != nil {
		return fmt.Errorf("fswatcher: lookup %s: %w", name, err)
	}
	if sym == 0 {
		return fmt.Errorf("fswatcher: lookup %s: symbol missing", name)
	}
	_nativeRegisterFunc(fptr, sym)
	return nil
}

func registerOptionalNativeFunc(fptr any, handle uintptr, name string) error {
	if err := registerNativeFunc(fptr, handle, name); err != nil {
		return err
	}
	return nil
}

func newCFString(s string) (uintptr, error) {
	ref := _cfStringCreateWithCString(0, s, kCFStringEncodingUTF8)
	if ref == 0 {
		return 0, fmt.Errorf("fswatcher: CFStringCreateWithCString failed")
	}
	return ref, nil
}

func newCFPathArray(path string) (uintptr, func(), error) {
	cfPath, err := newCFString(path)
	if err != nil {
		return 0, nil, err
	}

	arr := _cfArrayCreateMutable(0, 1, cfTypeArrayCallBacks)
	if arr == 0 {
		releaseCF(cfPath)
		return 0, nil, fmt.Errorf("fswatcher: CFArrayCreateMutable failed")
	}
	_cfArrayAppendValue(arr, cfPath)
	releaseCF(cfPath)

	cleanup := func() {
		releaseCF(arr)
	}
	return arr, cleanup, nil
}

func newDispatchQueue(label string) (uintptr, error) {
	queue := _dispatchQueueCreate(label, 0)
	if queue == 0 {
		return 0, fmt.Errorf("fswatcher: dispatch_queue_create failed")
	}
	return queue, nil
}

func createFSEventStream(watcherID, pathArray uintptr, callback uintptr) (uintptr, error) {
	if pathArray == 0 {
		return 0, fmt.Errorf("fswatcher: FSEventStreamCreate requires a path array")
	}
	if callback == 0 {
		return 0, fmt.Errorf("fswatcher: FSEventStreamCreate requires a callback")
	}

	ctx := fsEventStreamContext{Info: watcherID}
	flags := uint32(fseCreateFileEvents | fseCreateNoDefer | fseCreateWatchRoot)
	stream := _fseStreamCreate(
		0,
		callback,
		&ctx,
		pathArray,
		kFSEventStreamEventIdSinceNow,
		defaultLatency,
		flags,
	)
	if stream == 0 {
		return 0, fmt.Errorf("fswatcher: FSEventStreamCreate failed")
	}
	return stream, nil
}

func startFSEventStream(stream uintptr, queue uintptr) error {
	if stream == 0 {
		return fmt.Errorf("fswatcher: FSEventStreamStart requires a stream")
	}
	if queue == 0 {
		return fmt.Errorf("fswatcher: FSEventStreamStart requires a dispatch queue")
	}

	_fseStreamSetDispatchQueue(stream, queue)
	if _fseStreamStart(stream) == 0 {
		_fseStreamInvalidate(stream)
		_fseStreamRelease(stream)
		return fmt.Errorf("fswatcher: FSEventStreamStart failed")
	}
	return nil
}

func releaseCF(ref uintptr) {
	if ref != 0 {
		_cfRelease(ref)
	}
}

func releaseDispatchQueue(queue uintptr) {
	if queue != 0 && _dispatchRelease != nil {
		_dispatchRelease(queue)
	}
}

func releaseFSEventStream(stream uintptr) {
	if stream != 0 {
		_fseStreamStop(stream)
		_fseStreamInvalidate(stream)
		_fseStreamRelease(stream)
	}
}
