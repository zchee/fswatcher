//go:build darwin

package fswatcher

import "sync"

// darwinStream represents a single FSEventStream for one Add/AddRecursive call.
type darwinStream struct {
	mu        sync.Mutex
	stream    uintptr // FSEventStreamRef
	key       string
	path      string
	op        Op
	recursive bool
	isDir     bool
	stopped   bool
}

func (w *Watcher) createStreamLocked(path, key string, op Op, recursive, isDir bool) (*darwinStream, error) {
	pathArray, releasePathArray, err := newCFPathArray(path)
	if err != nil {
		return nil, err
	}
	defer releasePathArray()

	stream, err := createFSEventStream(w.id, pathArray, fseCallback)
	if err != nil {
		return nil, err
	}

	if err := startFSEventStream(stream, w.queue); err != nil {
		return nil, err
	}

	return &darwinStream{
		stream:    stream,
		key:       key,
		path:      path,
		op:        op,
		recursive: recursive,
		isDir:     isDir,
	}, nil
}

func (s *darwinStream) stop() {
	if s == nil {
		return
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	stream := s.stream
	s.stopped = true
	s.stream = 0
	s.mu.Unlock()

	releaseFSEventStream(stream)
}
