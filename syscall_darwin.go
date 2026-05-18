//go:build darwin

package fswatcher

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

//go:linkname syscall_syscall6 syscall.syscall6
func syscall_syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err unix.Errno)

//go:linkname errnoErr golang.org/x/sys/unix.errnoErr
func errnoErr(e unix.Errno) error

func getattrlist(path string, attrList *unix.Attrlist, attrBuf []byte, options int) (err error) {
	pathPtr, err := unix.BytePtrFromString(path)
	if err != nil {
		return err
	}

	var attrBufPtr unsafe.Pointer
	if len(attrBuf) > 0 {
		attrBufPtr = unsafe.Pointer(&attrBuf[0])
	} else {
		attrBufPtr = unsafe.Pointer(&getattrlistZero)
	}
	_, _, e1 := syscall_syscall6(
		libc_getattrlist_trampoline_addr,
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(attrList)),
		uintptr(attrBufPtr),
		uintptr(len(attrBuf)),
		uintptr(options),
		0,
	)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

// getattrlistZero is a single-word zero used when getattrlist receives an empty
// attribute buffer. Passing a valid pointer matches the generated x/sys syscall
// wrapper pattern for zero-length byte slices.
var getattrlistZero uintptr

var libc_getattrlist_trampoline_addr uintptr

//go:cgo_import_dynamic libc_getattrlist getattrlist "/usr/lib/libSystem.B.dylib"
