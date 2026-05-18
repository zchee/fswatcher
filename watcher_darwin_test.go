//go:build darwin

package fswatcher

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"
)

func TestMatchRegistrationPrefersMostSpecificNormalizedPath(t *testing.T) {
	parent := filepath.Join("/tmp", "Café")
	if !caseInsensitivePathForTest(t, parent) {
		t.Skip("case-insensitive normalized path matching test")
	}
	child := filepath.Join(parent, "nested")
	// Use a decomposed event path to prove the match logic compares the
	// normalized path key rather than raw bytes.
	eventPath := filepath.Join("/tmp", "Cafe\u0301", "nested")

	regs := []fseReg{
		fseRegForTest(parent, All, true, true),
		fseRegForTest(child, Write, false, false),
	}

	got, ok := matchRegistration(eventPath, regs)
	if !ok {
		t.Fatal("matchRegistration returned no match")
	}
	if got.path != child {
		t.Fatalf("matchRegistration(%q) = %+v, want child registration", eventPath, got)
	}
}

func TestMatchRegistrationPrefersMostSpecific(t *testing.T) {
	root := filepath.Join(tempDir(t), "root")
	child := filepath.Join(root, "child")

	regs := []fseReg{
		fseRegForTest(root, All, true, true),
		fseRegForTest(child, Write, false, false),
	}

	got, ok := matchRegistration(child, regs)
	if !ok {
		t.Fatalf("matchRegistration(%q) = ok=false", child)
	}
	if got.path != child {
		t.Fatalf("matchRegistration(%q) = %q, want %q", child, got.path, child)
	}

	sibling := filepath.Join(root, "sibling", "file.txt")
	got, ok = matchRegistration(sibling, regs)
	if !ok {
		t.Fatalf("matchRegistration(%q) = ok=false", sibling)
	}
	if got.path != root {
		t.Fatalf("matchRegistration(%q) = %q, want %q", sibling, got.path, root)
	}
}

func TestHandleFSEventsCallbackRootChangedUsesNormalizedLookup(t *testing.T) {
	w := &Watcher{
		events:      make(chan Event, 1),
		errors:      make(chan error, 1),
		internalEv:  make(chan Event, 1),
		internalErr: make(chan error, 1),
		streams:     make(map[string]*fsStream),
		done:        make(chan struct{}),
	}

	prevStop := _fseStreamStop
	prevInvalidate := _fseStreamInvalidate
	prevRelease := _fseStreamRelease
	defer func() {
		w.cleanupW.Wait()
		_fseStreamStop = prevStop
		_fseStreamInvalidate = prevInvalidate
		_fseStreamRelease = prevRelease
	}()

	_fseStreamStop = func(uintptr) {}
	_fseStreamInvalidate = func(uintptr) {}
	_fseStreamRelease = func(uintptr) {}

	w.id = registerWatcher(w)
	defer unregisterWatcher(w.id)

	watched := filepath.Join("/tmp", "Café")
	if !caseInsensitivePathForTest(t, watched) {
		t.Skip("case-insensitive normalized root-change lookup test")
	}
	key := pathKey(watched)
	w.streams[key] = &fsStream{
		stream:    1,
		path:      watched,
		key:       key,
		keyLen:    len(key),
		op:        Rename | Remove,
		recursive: true,
		isDir:     true,
	}

	eventPath := []byte(filepath.Join("/tmp", "Cafe\u0301") + "\x00")
	pathPtrs := []unsafe.Pointer{unsafe.Pointer(&eventPath[0])}
	flags := []uint32{fseRootChanged}

	handleFSEventsCallback(w.id, 1, unsafe.Pointer(&pathPtrs[0]), unsafe.Pointer(&flags[0]))
	w.cleanupW.Wait()

	if _, ok := w.streams[key]; ok {
		t.Fatalf("RootChanged did not remove normalized stream key %q", key)
	}

	select {
	case ev := <-w.internalEv:
		if ev.Name != watched {
			t.Fatalf("event name = %q, want %q", ev.Name, watched)
		}
		if ev.Op != (Rename | Remove) {
			t.Fatalf("event op = %v, want %v", ev.Op, Rename|Remove)
		}
	default:
		t.Fatal("expected RootChanged event")
	}
}

func fseRegForTest(path string, op Op, recursive, isDir bool) fseReg {
	key := pathKey(path)
	return fseReg{
		path:      path,
		key:       key,
		keyLen:    len(key),
		op:        op,
		recursive: recursive,
		isDir:     isDir,
	}
}

func TestWatchRenameReportsCompleteBatch(t *testing.T) {
	dir := tempDir(t)
	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(oldPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w := newWatcher(t)
	if err := w.Add(dir, Rename|Create); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	events := collectEvents(t, w, 2)
	var sawRenameOld bool
	var sawCreateNew bool
	var sawCreateOld bool
	for _, ev := range events {
		switch {
		case ev.Name == oldPath && ev.Op.Has(Rename):
			sawRenameOld = true
		case ev.Name == newPath && ev.Op.Has(Create):
			sawCreateNew = true
		case ev.Name == oldPath && ev.Op.Has(Create):
			sawCreateOld = true
		default:
			t.Fatalf("unexpected rename batch event: %v", ev)
		}
	}
	if !sawRenameOld {
		t.Fatalf("missing Rename for %q; got %v", oldPath, events)
	}
	if !sawCreateNew && !sawCreateOld {
		t.Fatalf("missing destination Create; got %v", events)
	}
	assertNoEvents(t, w, 250*time.Millisecond)
}

func TestWatchRenameOnlyReportsSourceWhenCreateIsNotRequested(t *testing.T) {
	dir := tempDir(t)
	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(oldPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w := newWatcher(t)
	if err := w.Add(dir, Rename); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	ev := waitOp(t, w, Rename)
	if ev.Name != oldPath {
		t.Fatalf("Name = %q, want old path %q", ev.Name, oldPath)
	}
	assertNoEvents(t, w, 250*time.Millisecond)
}

func TestAddUnicodeNormalization(t *testing.T) {
	parent := tempDir(t)
	if !caseInsensitivePathForTest(t, parent) {
		t.Skip("case-insensitive Unicode-normalization contract test")
	}
	w := newWatcher(t)

	dir := filepath.Join(parent, "\u304C") // HIRAGANA LETTER GA (U+304C) in NFC
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := w.Add(dir, All); err != nil {
		t.Fatalf("Add: %v", err)
	}

	dir = filepath.Join(parent, "\u304B\u3099") // HIRAGANA LETTER GA in NFD (U+304C decomposes to U+304B + U+3099)
	if err := os.Mkdir(dir, 0o755); !errors.Is(err, os.ErrExist) {
		t.Errorf("Mkdir(\u304B\u3099) = %v, want os.ErrExist", err)
	}
	if err := w.Add(dir, All); !errors.Is(err, ErrAlreadyAdded) {
		t.Errorf("Add(\u304B\u3099) = %v, want ErrAlreadyAdded", err)
	}
	if err := w.Remove(dir); err != nil {
		t.Errorf("Remove(\u304B\u3099) = %v, want nil", err)
	}
}

func TestAddRecursiveOverlapContracts(t *testing.T) {
	if os.Getenv("FSWATCHER_DARWIN_STRICT_CONTRACT_TESTS") == "" {
		t.Skip("set FSWATCHER_DARWIN_STRICT_CONTRACT_TESTS=1 to run the live recursive-overlap contract test")
	}

	root := tempDir(t)
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	w := newWatcher(t)
	if err := w.AddRecursive(root, All); err != nil {
		t.Fatalf("AddRecursive(root): %v", err)
	}

	if err := w.Add(child, All); !errors.Is(err, ErrAlreadyAdded) {
		t.Fatalf("Add(child) = %v, want ErrAlreadyAdded", err)
	}
	if err := w.AddRecursive(child, All); !errors.Is(err, ErrAlreadyAdded) {
		t.Fatalf("AddRecursive(child) = %v, want ErrAlreadyAdded", err)
	}
	if err := w.Remove(child); !errors.Is(err, ErrNotAdded) {
		t.Fatalf("Remove(child) = %v, want ErrNotAdded", err)
	}
}
