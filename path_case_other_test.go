//go:build !darwin

package fswatcher

import (
	"runtime"
	"testing"
)

func caseInsensitivePathForTest(t testing.TB, _ string) bool {
	t.Helper()
	return runtime.GOOS == "windows"
}
