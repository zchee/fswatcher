//go:build darwin

package fswatcher

import (
	"encoding/binary"
	"errors"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestGetattrlistRejectsInvalidPathStrings(t *testing.T) {
	attr := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT}
	path := "/tmp/fswatcher\x00suffix"

	err := getattrlist(path, &attr, nil, 0)
	if !errors.Is(err, unix.EINVAL) {
		t.Fatalf("getattrlist(%q) = %v, want %v", path, err, unix.EINVAL)
	}
}

func TestGetattrlistHandlesEmptyAttrBuffer(t *testing.T) {
	attr := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Volattr:     unix.ATTR_VOL_CAPABILITIES,
	}

	if err := getattrlist(tempDir(t), &attr, nil, 0); err == nil {
		t.Fatal("getattrlist with empty attrBuf succeeded, want an error")
	}
}

func TestGetattrlistReadsVolumeCapabilities(t *testing.T) {
	attr := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Volattr:     unix.ATTR_VOL_CAPABILITIES,
	}
	buf := make([]byte, 4+unsafe.Sizeof(darwinVolumeCapabilitiesAttr{}))

	if err := getattrlist(tempDir(t), &attr, buf, 0); err != nil {
		t.Fatalf("getattrlist volume capabilities: %v", err)
	}
	if gotLen, wantMin := int(binary.LittleEndian.Uint32(buf[:4])), int(unsafe.Sizeof(darwinVolumeCapabilitiesAttr{})); gotLen < wantMin {
		t.Fatalf("getattrlist returned length %d, want at least %d", gotLen, wantMin)
	}

	caps := (*darwinVolumeCapabilitiesAttr)(unsafe.Pointer(&buf[4]))
	if caps.Valid[0]&darwinVolCapFmtCaseSensitive == 0 {
		t.Fatalf("volume capabilities valid bits = %#x, missing case-sensitive capability bit", caps.Valid[0])
	}
}
