//go:build darwin

package fswatcher

import (
	"path/filepath"
	"testing"
)

func TestDarwinPathKeyCaseSensitivePolicyKeepsDistinctNames(t *testing.T) {
	policy := darwinPathPolicy{caseSensitive: true}

	lower := "/tmp/fswatcher/foo"
	upper := "/tmp/fswatcher/FOO"
	if darwinPathKeyWithPolicy(lower, policy) == darwinPathKeyWithPolicy(upper, policy) {
		t.Fatalf("case-sensitive policy collapsed %q and %q", lower, upper)
	}

	nfc := "/tmp/fswatcher/\u304C"
	nfd := "/tmp/fswatcher/\u304B\u3099"
	if darwinPathKeyWithPolicy(nfc, policy) == darwinPathKeyWithPolicy(nfd, policy) {
		t.Fatalf("case-sensitive policy collapsed %q and %q", nfc, nfd)
	}
}

func TestDarwinPathKeyCaseInsensitivePolicyFoldsCaseAndUnicode(t *testing.T) {
	policy := darwinPathPolicy{caseSensitive: false}

	lower := "/tmp/fswatcher/foo"
	upper := "/tmp/fswatcher/FOO"
	if darwinPathKeyWithPolicy(lower, policy) != darwinPathKeyWithPolicy(upper, policy) {
		t.Fatalf("case-insensitive policy kept %q and %q distinct", lower, upper)
	}

	sharpS := "/tmp/fswatcher/ß"
	ss := "/tmp/fswatcher/ss"
	if darwinPathKeyWithPolicy(sharpS, policy) != darwinPathKeyWithPolicy(ss, policy) {
		t.Fatalf("case-insensitive policy kept %q and %q distinct", sharpS, ss)
	}

	nfc := "/tmp/fswatcher/\u304C"
	nfd := "/tmp/fswatcher/\u304B\u3099"
	if darwinPathKeyWithPolicy(nfc, policy) != darwinPathKeyWithPolicy(nfd, policy) {
		t.Fatalf("case-insensitive policy kept %q and %q distinct", nfc, nfd)
	}
}

func TestDarwinPathKeyMatchesVolumePolicy(t *testing.T) {
	dir := tempDir(t)

	sensitive := darwinPathPolicyForPath(dir).caseSensitive

	lower := filepath.Join(dir, "foo")
	upper := filepath.Join(dir, "FOO")
	if sensitive {
		if pathKey(lower) == pathKey(upper) {
			t.Fatalf("pathKey(%q) == pathKey(%q) on a case-sensitive volume", lower, upper)
		}
	} else {
		if pathKey(lower) != pathKey(upper) {
			t.Fatalf("pathKey(%q) != pathKey(%q) on a case-insensitive volume", lower, upper)
		}
	}

	nfc := filepath.Join(dir, "\u304C")
	nfd := filepath.Join(dir, "\u304B\u3099")
	if sensitive {
		if pathKey(nfc) == pathKey(nfd) {
			t.Fatalf("pathKey(%q) == pathKey(%q) on a case-sensitive volume", nfc, nfd)
		}
	} else {
		if pathKey(nfc) != pathKey(nfd) {
			t.Fatalf("pathKey(%q) != pathKey(%q) on a case-insensitive volume", nfc, nfd)
		}
	}
}

func TestDarwinUnicodeFoldKey(t *testing.T) {
	if got, want := unicodeFoldKey("FOO"), "foo"; got != want {
		t.Fatalf("unicodeFoldKey(%q) = %q, want %q", "FOO", got, want)
	}

	gotNFC := unicodeFoldKey(filepath.Join("/tmp", "Café"))
	gotNFD := unicodeFoldKey(filepath.Join("/tmp", "Cafe\u0301"))
	if gotNFC != gotNFD {
		t.Fatalf("unicodeFoldKey NFC/NFD mismatch: %q vs %q", gotNFC, gotNFD)
	}
}

func TestDarwinMountForPathFallsBackToParent(t *testing.T) {
	dir := tempDir(t)
	nested := filepath.Join(dir, "missing", "child")

	gotNested, err := darwinMountForPath(nested)
	if err != nil {
		t.Fatalf("darwinMountForPath(%q): %v", nested, err)
	}
	gotDir, err := darwinMountForPath(dir)
	if err != nil {
		t.Fatalf("darwinMountForPath(%q): %v", dir, err)
	}
	if gotNested != gotDir {
		t.Fatalf("darwinMountForPath(%q) = %q, want %q", nested, gotNested, gotDir)
	}
}
