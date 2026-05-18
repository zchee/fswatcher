//go:build darwin

package fswatcher

import (
	"bytes"
	"encoding/binary"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/unix"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const darwinVolCapFmtCaseSensitive = 0x00000100

type darwinPathPolicy struct {
	caseSensitive bool
}

type darwinVolumeCapabilitiesAttr struct {
	Capabilities [4]uint32
	Valid        [4]uint32
}

var darwinPathPolicyCache sync.Map // map[string]darwinPathPolicy, keyed by mount path

// pathKey returns a comparison key for p that follows the storage policy of the
// volume containing p. Case-sensitive Darwin volumes must not collapse names the
// filesystem keeps distinct; detection failures therefore use a conservative
// case-sensitive key.
func pathKey(p string) string {
	return darwinPathKeyWithPolicy(p, darwinPathPolicyForPath(p))
}

func darwinPathKeyWithPolicy(p string, policy darwinPathPolicy) string {
	if policy.caseSensitive {
		return p
	}
	return unicodeFoldKey(p)
}

// canonicalizeOS keeps the platform-neutral canonicalization pipeline intact on
// Darwin. Path identity normalization happens in pathKey because it depends on
// the containing volume.
func canonicalizeOS(p string) string {
	return p
}

func darwinPathPolicyForPath(path string) darwinPathPolicy {
	mount, err := darwinMountForPath(path)
	if err != nil {
		return darwinConservativePathPolicy()
	}
	if cached, ok := darwinPathPolicyCache.Load(mount); ok {
		return cached.(darwinPathPolicy)
	}

	policy := darwinConservativePathPolicy()
	if sensitive, err := darwinCaseSensitiveMount(mount); err == nil {
		policy.caseSensitive = sensitive
	}
	actual, _ := darwinPathPolicyCache.LoadOrStore(mount, policy)
	return actual.(darwinPathPolicy)
}

func darwinConservativePathPolicy() darwinPathPolicy {
	return darwinPathPolicy{caseSensitive: true}
}

func darwinMountForPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	var lastErr error
	for {
		var stat unix.Statfs_t
		err := unix.Statfs(cleaned, &stat)
		if err == nil {
			return darwinCString(stat.Mntonname[:]), nil
		}
		lastErr = err
		if cleaned == string(filepath.Separator) {
			return "", lastErr
		}
		parent := filepath.Dir(cleaned)
		if parent == cleaned {
			return "", lastErr
		}
		cleaned = parent
	}
}

func darwinCaseSensitiveMount(mount string) (bool, error) {
	attr := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Volattr:     unix.ATTR_VOL_CAPABILITIES,
	}
	buf := make([]byte, 4+unsafe.Sizeof(darwinVolumeCapabilitiesAttr{}))
	if err := getattrlist(mount, &attr, buf, 0); err != nil {
		return false, err
	}
	if len(buf) < 4+int(unsafe.Sizeof(darwinVolumeCapabilitiesAttr{})) {
		return false, unix.EINVAL
	}
	if int(binary.LittleEndian.Uint32(buf[:4])) < int(unsafe.Sizeof(darwinVolumeCapabilitiesAttr{})) {
		return false, unix.EIO
	}

	caps := (*darwinVolumeCapabilitiesAttr)(unsafe.Pointer(&buf[4]))
	if caps.Valid[0]&darwinVolCapFmtCaseSensitive == 0 {
		return false, unix.EIO
	}
	return caps.Capabilities[0]&darwinVolCapFmtCaseSensitive != 0, nil
}

func darwinCString(b []byte) string {
	if before, _, ok := bytes.Cut(b, []byte{0}); ok {
		return string(before)
	}
	return string(b)
}

// unicodeFoldKey is separated for tests to make the case-insensitive mapping
// explicit and to keep pathKey free of extra conditionals.
func unicodeFoldKey(p string) string {
	if p == "" {
		return ""
	}
	if darwinIsASCII(p) {
		return strings.ToLower(p)
	}
	return norm.NFC.String(cases.Fold().String(p))
}

func darwinIsASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
