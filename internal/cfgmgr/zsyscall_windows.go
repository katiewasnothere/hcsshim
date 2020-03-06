// Code generated mksyscall_windows.exe DO NOT EDIT

package cfgmgr

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var _ unsafe.Pointer

// Do the interface allocations only once for common
// Errno values.
const (
	errnoERROR_IO_PENDING = 997
)

var (
	errERROR_IO_PENDING error = syscall.Errno(errnoERROR_IO_PENDING)
)

// errnoErr returns common boxed Errno values, to prevent
// allocations at runtime.
func errnoErr(e syscall.Errno) error {
	switch e {
	case 0:
		return nil
	case errnoERROR_IO_PENDING:
		return errERROR_IO_PENDING
	}
	// TODO: add more here, after collecting data on the common
	// error values see on Windows. (perhaps when running
	// all.bat?)
	return e
}

var (
	modcfgmgr32 = windows.NewLazySystemDLL("cfgmgr32.dll")

	procCM_Get_Device_ID_List_SizeA = modcfgmgr32.NewProc("CM_Get_Device_ID_List_SizeA")
	procCM_Get_Device_ID_ListA      = modcfgmgr32.NewProc("CM_Get_Device_ID_ListA")
)

func cmGetDeviceIDListSize(pulLen *uint32, pszFilter uintptr, uFlags uint64) (hr error) {
	r0, _, _ := syscall.Syscall(procCM_Get_Device_ID_List_SizeA.Addr(), 3, uintptr(unsafe.Pointer(pulLen)), uintptr(pszFilter), uintptr(uFlags))
	if int32(r0) < 0 {
		if r0&0x1fff0000 == 0x00070000 {
			r0 &= 0xffff
		}
		hr = syscall.Errno(r0)
	}
	return
}

func cmGetDeviceIDList(pszFilter uintptr, buffer *byte, bufferLen uint64, uFlags uint64) (hr error) {
	r0, _, _ := syscall.Syscall6(procCM_Get_Device_ID_ListA.Addr(), 4, uintptr(pszFilter), uintptr(unsafe.Pointer(buffer)), uintptr(bufferLen), uintptr(uFlags), 0, 0)
	if int32(r0) < 0 {
		if r0&0x1fff0000 == 0x00070000 {
			r0 &= 0xffff
		}
		hr = syscall.Errno(r0)
	}
	return
}
