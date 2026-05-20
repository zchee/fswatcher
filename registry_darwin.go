//go:build darwin

package fswatcher

import (
	"path/filepath"
	"strings"
	"sync"
)

// darwinRegistration captures the immutable data needed by the callback
// to match an event path against a registered watcher stream.
type darwinRegistration struct {
	key       string
	path      string
	op        Op
	recursive bool
	isDir     bool
}

var (
	darwinWatcherRegistryMu sync.Mutex
	darwinWatcherRegistry   = map[uintptr]*Watcher{}
	darwinNextWatcherID     uintptr
)

func registerDarwinWatcher(w *Watcher) uintptr {
	darwinWatcherRegistryMu.Lock()
	defer darwinWatcherRegistryMu.Unlock()

	darwinNextWatcherID++
	darwinWatcherRegistry[darwinNextWatcherID] = w
	return darwinNextWatcherID
}

func unregisterDarwinWatcher(id uintptr) {
	darwinWatcherRegistryMu.Lock()
	defer darwinWatcherRegistryMu.Unlock()

	delete(darwinWatcherRegistry, id)
}

func lookupDarwinWatcher(id uintptr) *Watcher {
	darwinWatcherRegistryMu.Lock()
	defer darwinWatcherRegistryMu.Unlock()

	return darwinWatcherRegistry[id]
}

// darwinRegistry owns the Darwin watcher registration map and its
// path identity policy.
type darwinRegistry struct {
	mu       sync.Mutex
	identity *darwinPathPolicy
	streams  map[string]*darwinStream
}

func newDarwinRegistry() *darwinRegistry {
	return &darwinRegistry{
		identity: newDarwinPathPolicy(),
		streams:  make(map[string]*darwinStream),
	}
}

func (r *darwinRegistry) keyFor(path string) string {
	if r != nil && r.identity != nil {
		return r.identity.key(path)
	}
	return pathKey(path)
}

func (r *darwinRegistry) add(stream *darwinStream) error {
	if stream == nil {
		return ErrNotAdded
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.streams[stream.key]; exists {
		return ErrAlreadyAdded
	}
	r.streams[stream.key] = stream
	return nil
}

func (r *darwinRegistry) exists(path string) bool {
	return r.existsKey(r.keyFor(path))
}

func (r *darwinRegistry) existsKey(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.streams[key]
	return ok
}

func (r *darwinRegistry) remove(path string) (*darwinStream, bool) {
	abs, err := canonicalize(path)
	if err != nil {
		return nil, false
	}
	return r.removeKey(r.keyFor(abs))
}

func (r *darwinRegistry) removeKey(key string) (*darwinStream, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stream, ok := r.streams[key]
	if !ok {
		return nil, false
	}
	delete(r.streams, key)
	return stream, true
}

func (r *darwinRegistry) snapshot() []darwinRegistration {
	r.mu.Lock()
	defer r.mu.Unlock()

	regs := make([]darwinRegistration, 0, len(r.streams))
	for _, stream := range r.streams {
		regs = append(regs, darwinRegistration{
			key:       stream.key,
			path:      stream.path,
			op:        stream.op,
			recursive: stream.recursive,
			isDir:     stream.isDir,
		})
	}
	return regs
}

func (r *darwinRegistry) snapshotAndClear() []*darwinStream {
	return r.drain()
}

func (r *darwinRegistry) drain() []*darwinStream {
	r.mu.Lock()
	defer r.mu.Unlock()

	streams := make([]*darwinStream, 0, len(r.streams))
	for key, stream := range r.streams {
		streams = append(streams, stream)
		delete(r.streams, key)
	}
	return streams
}

func (r *darwinRegistry) match(path string, regs []darwinRegistration) (darwinRegistration, bool) {
	pk := r.keyFor(path)
	var best darwinRegistration
	found := false
	for _, reg := range regs {
		rk := reg.key
		if pk == rk || isUnder(pk, rk) {
			if !found || len(rk) > len(best.key) {
				best = reg
				found = true
			}
		}
	}
	return best, found
}

func isUnder(child, parent string) bool {
	if parent == "/" {
		return true
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}
