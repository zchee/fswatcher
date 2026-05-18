//go:build darwin

package fswatcher

import "testing"

func caseInsensitivePathForTest(t testing.TB, p string) bool {
	t.Helper()
	return !darwinPathPolicyForPath(p).caseSensitive
}
