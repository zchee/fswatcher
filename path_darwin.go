//go:build darwin

package fswatcher

import (
	"errors"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
	"golang.org/x/text/cases"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	darwinAttrVolCapabilities      = 0x00020000
	darwinVolCapabilitiesFormat    = 0
	darwinVolCapFmtCaseSensitive   = 0x00000100
	darwinVolCapFmtCasePreserving  = 0x00000200
	darwinVolumeCapabilitiesLength = 4 + 4*4 + 4*4
)

// darwinVolumeCapabilities mirrors the attrBuf layout returned by
// getattrlist(2) for ATTR_VOL_CAPABILITIES: a uint32 total length followed by
// vol_capabilities_attr_t.
type darwinVolumeCapabilities struct {
	Length       uint32
	Capabilities [4]uint32
	Valid        [4]uint32
}

type darwinVolumeIdentity struct {
	caseSensitive            bool
	normalizationInsensitive bool
}

type darwinVolumeKey struct {
	fsid       unix.Fsid
	mountPoint string
}

// darwinPathPolicy computes comparison keys for paths on Darwin volumes. It
// caches one identity per mount so repeated registry lookups avoid syscalls.
type darwinPathPolicy struct {
	mu     sync.Mutex
	cache  map[darwinVolumeKey]darwinVolumeIdentity
	detect func(string) (darwinVolumeIdentity, error)
}

var defaultDarwinPathPolicy = newDarwinPathPolicy()

// pathKey returns a comparison key for p using the current Darwin volume's path
// identity policy. Detection failures use a conservative case-sensitive policy
// so distinct names are not conflated.
func pathKey(p string) string {
	return defaultDarwinPathPolicy.key(p)
}

func newDarwinPathPolicy() *darwinPathPolicy {
	return &darwinPathPolicy{
		cache:  make(map[darwinVolumeKey]darwinVolumeIdentity),
		detect: darwinDetectVolumeIdentity,
	}
}

func (p *darwinPathPolicy) key(path string) string {
	return p.identity(path).key(path)
}

func (p *darwinPathPolicy) identity(path string) darwinVolumeIdentity {
	if p == nil {
		return darwinCaseSensitiveVolumeIdentity()
	}

	key, err := darwinVolumeKeyForPath(path)
	if err != nil {
		return darwinCaseSensitiveVolumeIdentity()
	}

	p.mu.Lock()
	if identity, ok := p.cache[key]; ok {
		p.mu.Unlock()
		return identity
	}
	p.mu.Unlock()

	detect := p.detect
	if detect == nil {
		detect = darwinDetectVolumeIdentity
	}

	identity, err := detect(key.mountPoint)
	if err != nil {
		identity = darwinCaseSensitiveVolumeIdentity()
	}

	p.mu.Lock()
	p.cache[key] = identity
	p.mu.Unlock()
	return identity
}

func darwinCaseSensitiveVolumeIdentity() darwinVolumeIdentity {
	return darwinVolumeIdentity{caseSensitive: true}
}

func darwinCaseInsensitiveVolumeIdentity() darwinVolumeIdentity {
	return darwinVolumeIdentity{
		caseSensitive:            false,
		normalizationInsensitive: true,
	}
}

func (i darwinVolumeIdentity) key(path string) string {
	return normalizeDarwinPath(path, i.caseSensitive, i.normalizationInsensitive)
}

func normalizeDarwinPath(path string, caseSensitive bool, normalizationInsensitive ...bool) string {
	normalize := !caseSensitive
	if len(normalizationInsensitive) > 0 {
		normalize = normalize || normalizationInsensitive[0]
	}
	switch {
	case caseSensitive && !normalize:
		return path
	case caseSensitive:
		return norm.NFD.String(path)
	case normalize:
		path, _, _ = transform.String(transform.Chain(norm.NFD, cases.Fold()), path)
		return path
	default:
		path, _, _ = transform.String(cases.Fold(), path)
		return path
	}
}

func darwinVolumeKeyForPath(path string) (darwinVolumeKey, error) {
	if path == "" {
		path = "."
	}
	probe := filepath.Clean(path)
	for {
		var stat unix.Statfs_t
		if err := unix.Statfs(probe, &stat); err == nil {
			return darwinVolumeKey{
				fsid:       stat.Fsid,
				mountPoint: unix.ByteSliceToString(stat.Mntonname[:]),
			}, nil
		} else if !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ENOTDIR) {
			return darwinVolumeKey{}, err
		} else {
			parent := filepath.Dir(probe)
			if parent == probe {
				return darwinVolumeKey{}, err
			}
			probe = parent
		}
	}
}

func darwinDetectVolumeIdentity(path string) (darwinVolumeIdentity, error) {
	caseSensitive, err := darwinVolumeCaseSensitive(path)
	if err != nil {
		return darwinVolumeIdentity{}, err
	}
	if caseSensitive {
		return darwinCaseSensitiveVolumeIdentity(), nil
	}
	return darwinCaseInsensitiveVolumeIdentity(), nil
}

func darwinVolumeCaseSensitive(path string) (bool, error) {
	attrList := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Volattr:     darwinAttrVolCapabilities,
	}
	var caps darwinVolumeCapabilities

	if err := getattrlist(path, &attrList, unsafe.Pointer(&caps), unsafe.Sizeof(caps), 0); err != nil {
		return false, err
	}
	if caps.Length < darwinVolumeCapabilitiesLength {
		return false, errors.New("fswatcher: short ATTR_VOL_CAPABILITIES response")
	}

	valid := caps.Valid[darwinVolCapabilitiesFormat]
	if valid&darwinVolCapFmtCaseSensitive == 0 {
		return true, nil
	}
	// CASE_PRESERVING describes how names are stored; CASE_SENSITIVE defines
	// equality. Keep the constant local to document the SDK bit used by this
	// policy even though it is not required for equality decisions.
	_ = darwinVolCapFmtCasePreserving
	return caps.Capabilities[darwinVolCapabilitiesFormat]&darwinVolCapFmtCaseSensitive != 0, nil
}

func getattrlist(path string, attrList *unix.Attrlist, attrBuf unsafe.Pointer, attrBufSize uintptr, options int) error {
	pathp, err := unix.BytePtrFromString(path)
	if err != nil {
		return err
	}
	_, _, errno := unix.Syscall6(
		unix.SYS_GETATTRLIST,
		uintptr(unsafe.Pointer(pathp)),
		uintptr(unsafe.Pointer(attrList)),
		uintptr(attrBuf),
		attrBufSize,
		uintptr(options),
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

// canonicalizeOS is a no-op on platforms without 8.3 short-form aliases.
func canonicalizeOS(p string) string {
	return p
}
