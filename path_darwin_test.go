//go:build darwin

package fswatcher

import (
	"sync/atomic"
	"testing"
)

func TestNormalizeDarwinPathCaseSensitivity(t *testing.T) {
	t.Parallel()

	left := "/Volumes/Work/README"
	right := "/volumes/work/readme"
	unicodeLeft := "/Volumes/Work/Café"
	unicodeRight := "/volumes/work/Café"

	if gotLeft, gotRight := normalizeDarwinPath(left, false), normalizeDarwinPath(right, false); gotLeft != gotRight {
		t.Fatalf("normalizeDarwinPath(case-insensitive) = %q, %q; want equal", gotLeft, gotRight)
	}
	if gotLeft, gotRight := normalizeDarwinPath(unicodeLeft, false), normalizeDarwinPath(unicodeRight, false); gotLeft != gotRight {
		t.Fatalf("normalizeDarwinPath(unicode case-insensitive) = %q, %q; want equal", gotLeft, gotRight)
	}

	if gotLeft, gotRight := normalizeDarwinPath(left, true), normalizeDarwinPath(right, true); gotLeft == gotRight {
		t.Fatalf("normalizeDarwinPath(case-sensitive) = %q, %q; want different", gotLeft, gotRight)
	}
}

func TestDarwinPathPolicyCachesIdentityByVolume(t *testing.T) {
	t.Parallel()

	var calls int32
	policy := &darwinPathPolicy{
		cache: make(map[darwinVolumeKey]darwinVolumeIdentity),
		detect: func(string) (darwinVolumeIdentity, error) {
			atomic.AddInt32(&calls, 1)
			return darwinVolumeIdentity{caseSensitive: false}, nil
		},
	}
	dir := t.TempDir()

	got1 := policy.key(dir)
	got2 := policy.key(dir)
	if got1 != got2 {
		t.Fatalf("policy.key repeated = %q, %q; want equal", got1, got2)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("detector calls = %d, want 1", got)
	}
}

func TestDarwinPathPolicyFallsBackConservativelyOnDetectionFailure(t *testing.T) {
	t.Parallel()

	policy := &darwinPathPolicy{
		cache: make(map[darwinVolumeKey]darwinVolumeIdentity),
		detect: func(string) (darwinVolumeIdentity, error) {
			return darwinVolumeIdentity{}, testingErr{}
		},
	}
	left := policy.key("/Volumes/Work/README")
	right := policy.key("/volumes/work/readme")
	if left == right {
		t.Fatalf("policy.key fallback = %q, %q; want conservative case-sensitive difference", left, right)
	}
}

type testingErr struct{}

func (testingErr) Error() string { return "forced failure" }
