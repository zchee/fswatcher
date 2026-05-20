//go:build darwin

package fswatcher

import (
	"fmt"
	"os"
	"sync"
	"unsafe"
)

// Create flags.
const (
	fseCreateNoDefer    = 0x02
	fseCreateWatchRoot  = 0x04
	fseCreateFileEvents = 0x10
)

const (
	kCFStringEncodingUTF8         = 0x08000100
	kFSEventStreamEventIdSinceNow = ^uint64(0)
	defaultLatency                = 0.01 // 10ms
)

// fsEventStreamContext mirrors the C FSEventStreamContext struct layout.
type fsEventStreamContext struct {
	Version         int64   // CFIndex = long on 64-bit
	Info            uintptr // void* — our watcher ID
	Retain          uintptr // NULL
	Release         uintptr // NULL
	CopyDescription uintptr // NULL
}

// Watcher monitors registered paths via macOS FSEvents.
type Watcher struct {
	// Events delivers change notifications. Closed when Close returns.
	Events <-chan Event
	// Errors delivers non-fatal errors from the read loop. Closed when Close returns.
	Errors <-chan error

	events chan<- Event
	errors chan<- error

	mu          sync.Mutex
	id          uintptr
	queue       uintptr // dispatch_queue_t
	registry    *darwinRegistry
	cleanupW    sync.WaitGroup
	internalEv  chan Event
	internalErr chan error
	closed      bool
	done        chan struct{}
	exited      chan struct{}
}

func registerWatcher(w *Watcher) uintptr {
	return registerDarwinWatcher(w)
}

func unregisterWatcher(id uintptr) {
	unregisterDarwinWatcher(id)
}

func lookupWatcher(id uintptr) *Watcher {
	return lookupDarwinWatcher(id)
}

// NewWatcher returns a Watcher backed by macOS FSEvents.
func NewWatcher() (*Watcher, error) {
	if err := initFSEvents(); err != nil {
		return nil, err
	}

	queue, err := newDispatchQueue("github.com/fswatcher/fswatcher\x00")
	if err != nil {
		return nil, err
	}

	events := make(chan Event, 64)
	errors := make(chan error, 8)
	w := &Watcher{
		Events:      events,
		Errors:      errors,
		events:      events,
		errors:      errors,
		queue:       queue,
		registry:    newDarwinRegistry(),
		internalEv:  make(chan Event, 256),
		internalErr: make(chan error, 8),
		done:        make(chan struct{}),
		exited:      make(chan struct{}),
	}
	w.id = registerWatcher(w)
	go w.readLoop()
	return w, nil
}

// readLoop drains the internal channel and forwards events to the
// public Events channel. It is the sole goroutine that closes Events,
// Errors, and exited, matching the pattern of the other backends.
func (w *Watcher) readLoop() {
	defer close(w.exited)
	defer close(w.events)
	defer close(w.errors)

	for {
		select {
		case ev := <-w.internalEv:
			select {
			case w.events <- ev:
			case <-w.done:
				return
			}
		case err := <-w.internalErr:
			select {
			case w.errors <- err:
			case <-w.done:
				return
			}
		case <-w.done:
			return
		}
	}
}

// Add registers path with the given event mask. Returns ErrAlreadyAdded
// if path is already registered, or ErrClosed if the watcher is closed.
func (w *Watcher) Add(path string, op Op) error {
	return w.add(path, op, false)
}

// AddRecursive registers path and every directory below it. FSEvents
// natively supports recursive monitoring so no manual walk is needed.
// Returns ErrAlreadyAdded if path is already registered.
func (w *Watcher) AddRecursive(path string, op Op) error {
	return w.add(path, op, true)
}

func (w *Watcher) add(path string, op Op, recursive bool) error {
	if op == 0 {
		op = All
	}
	abs, err := canonicalize(path)
	if err != nil {
		return fmt.Errorf("fswatcher: add %s: %w", path, err)
	}

	isDir := false
	if fi, err := os.Stat(abs); err == nil {
		isDir = fi.IsDir()
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	key := w.registry.keyFor(abs)
	if w.registry.existsKey(key) {
		return ErrAlreadyAdded
	}

	stream, err := w.createStreamLocked(abs, key, op, recursive, isDir)
	if err != nil {
		return fmt.Errorf("fswatcher: add %s: %w", abs, err)
	}
	w.registry.add(stream)
	return nil
}

// Remove unregisters path. Returns ErrNotAdded if path is not registered.
func (w *Watcher) Remove(path string) error {
	abs, err := canonicalize(path)
	if err != nil {
		return fmt.Errorf("fswatcher: remove %s: %w", path, err)
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return ErrClosed
	}
	key := w.registry.keyFor(abs)
	fs, ok := w.registry.removeKey(key)
	if !ok {
		w.mu.Unlock()
		return ErrNotAdded
	}
	w.mu.Unlock()

	fs.stop()
	return nil
}

// Close stops the watcher. Subsequent calls are no-ops. Close blocks
// until the read loop has fully exited so callers can rely on
// Events/Errors being closed by the time Close returns.
func (w *Watcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		<-w.exited
		return nil
	}
	w.closed = true
	close(w.done)

	streams := w.registry.snapshotAndClear()
	queue := w.queue
	id := w.id
	w.mu.Unlock()

	for _, s := range streams {
		s.stop()
	}

	w.cleanupW.Wait()
	<-w.exited
	releaseDispatchQueue(queue)
	unregisterWatcher(id)
	return nil
}

// handleFSEventsCallback processes a batch of FSEvents notifications.
func handleFSEventsCallback(clientInfo uintptr, n int, pathsPtr, flagsPtr unsafe.Pointer) {
	w := lookupWatcher(clientInfo)
	if w == nil {
		return
	}

	w.mu.Lock()
	closed := w.closed
	w.mu.Unlock()
	if closed {
		return
	}

	raw := parseRawFSEvents(n, pathsPtr, flagsPtr)
	for _, ev := range translateFSEventBatch(raw, w.registry) {
		if ev.err != nil {
			w.sendError(ev.err)
		}
		if ev.rootChanged {
			w.mu.Lock()
			if !w.closed {
				if fs, exists := w.registry.removeKey(ev.rootKey); exists {
					w.cleanupW.Go(func() {
						fs.stop()
					})
				}
			}
			w.mu.Unlock()
		}
		if ev.event.Op != 0 {
			w.sendEvent(ev.event)
		}
	}
}

func (w *Watcher) sendEvent(e Event) {
	select {
	case w.internalEv <- e:
	case <-w.done:
	}
}

func (w *Watcher) sendError(err error) {
	select {
	case w.internalErr <- err:
	case <-w.done:
	}
}
