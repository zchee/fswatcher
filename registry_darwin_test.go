//go:build darwin

package fswatcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDarwinRegistrySnapshotPreservesOverlapAndExactRemoval(t *testing.T) {
	t.Parallel()

	registry := newDarwinRegistry()
	registry.identity = &darwinPathPolicy{
		cache: make(map[darwinVolumeKey]darwinVolumeIdentity),
		detect: func(string) (darwinVolumeIdentity, error) {
			return darwinVolumeIdentity{caseSensitive: false}, nil
		},
	}
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	root, err := canonicalize(root)
	if err != nil {
		t.Fatalf("canonicalize(root): %v", err)
	}
	key := registry.keyFor(root)
	stream := &darwinStream{key: key, path: root, op: Write, recursive: true, isDir: true}

	if err := registry.add(stream); err != nil {
		t.Fatalf("registry.add: %v", err)
	}

	if err := registry.add(&darwinStream{key: key, path: root}); err != ErrAlreadyAdded {
		t.Fatalf("registry.add duplicate = %v, want ErrAlreadyAdded", err)
	}

	removed, ok := registry.remove(root)
	if !ok {
		t.Fatal("registry.remove returned no stream")
	}
	if removed != stream {
		t.Fatalf("registry.remove returned %p, want %p", removed, stream)
	}

	if _, ok := registry.remove(root); ok {
		t.Fatal("registry.remove returned a stream after removal")
	}

	if err := registry.add(stream); err != nil {
		t.Fatalf("registry.add after remove: %v", err)
	}
	if got := registry.snapshot(); len(got) != 1 {
		t.Fatalf("registry.snapshot len = %d, want 1", len(got))
	}

	drained := registry.drain()
	if len(drained) != 1 || drained[0] != stream {
		t.Fatalf("registry.drain = %#v, want one stream %#v", drained, stream)
	}
	if got := registry.snapshot(); len(got) != 0 {
		t.Fatalf("registry.snapshot after drain len = %d, want 0", len(got))
	}
}

func TestDarwinRegistryKeyForUsesPathIdentityPolicy(t *testing.T) {
	t.Parallel()

	registry := newDarwinRegistry()
	path := t.TempDir()
	registryPath := filepath.Join(path, "README")
	if err := os.MkdirAll(registryPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	registryPath, err := canonicalize(registryPath)
	if err != nil {
		t.Fatalf("canonicalize(path): %v", err)
	}
	registry.identity = &darwinPathPolicy{
		cache: make(map[darwinVolumeKey]darwinVolumeIdentity),
		detect: func(string) (darwinVolumeIdentity, error) {
			return darwinVolumeIdentity{caseSensitive: false}, nil
		},
	}
	if got, want := registry.keyFor(registryPath), normalizeDarwinPath(registryPath, false); got != want {
		t.Fatalf("registry.keyFor(%q) = %q, want %q", registryPath, got, want)
	}
}

func TestDarwinRegistryMatchPrefersMostSpecificPath(t *testing.T) {
	t.Parallel()

	registry := newDarwinRegistry()
	registry.identity = &darwinPathPolicy{
		cache: make(map[darwinVolumeKey]darwinVolumeIdentity),
		detect: func(string) (darwinVolumeIdentity, error) {
			return darwinVolumeIdentity{caseSensitive: false}, nil
		},
	}
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	root, err := canonicalize(root)
	if err != nil {
		t.Fatalf("canonicalize(root): %v", err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	nested, err = canonicalize(nested)
	if err != nil {
		t.Fatalf("canonicalize(nested): %v", err)
	}

	rootStream := &darwinStream{key: registry.keyFor(root), path: root, op: Write, recursive: true, isDir: true}
	nestedStream := &darwinStream{key: registry.keyFor(nested), path: nested, op: Create, recursive: false, isDir: true}
	if err := registry.add(rootStream); err != nil {
		t.Fatalf("registry.add(root): %v", err)
	}
	if err := registry.add(nestedStream); err != nil {
		t.Fatalf("registry.add(nested): %v", err)
	}

	match, ok := registry.match(filepath.Join(nested, "child.txt"), registry.snapshot())
	if !ok {
		t.Fatal("registry.match returned no match")
	}
	if match.path != nested {
		t.Fatalf("registry.match selected %q, want %q", match.path, nested)
	}
	if match.op != Create {
		t.Fatalf("registry.match op = %s, want %s", match.op, Create)
	}
}

func TestDarwinRegistryMatchRespectsRegistryIdentityInEventTranslation(t *testing.T) {
	t.Parallel()

	registry := newDarwinRegistry()
	registry.identity = &darwinPathPolicy{
		cache: make(map[darwinVolumeKey]darwinVolumeIdentity),
		detect: func(string) (darwinVolumeIdentity, error) {
			return darwinVolumeIdentity{caseSensitive: true}, nil
		},
	}

	root := filepath.Join(t.TempDir(), "WatchedRoot")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	root, err := canonicalize(root)
	if err != nil {
		t.Fatalf("canonicalize(root): %v", err)
	}

	rootStream := &darwinStream{
		key:       registry.keyFor(root),
		path:      root,
		op:        Write,
		recursive: true,
		isDir:     true,
	}
	if err := registry.add(rootStream); err != nil {
		t.Fatalf("registry.add(root): %v", err)
	}

	eventPath := filepath.Join(strings.ToLower(root), "child.txt")
	translated := translateFSEventBatch([]rawFSEvent{{path: eventPath, flags: fseItemModified}}, registry)
	if len(translated) != 0 {
		t.Fatalf("translateFSEventBatch(%q) = %#v, want no matches under case-sensitive registry policy", eventPath, translated)
	}
}
