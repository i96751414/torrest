// +build windows

package diskusage

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	kernel32            syscall.Handle
	pGetDiskFreeSpaceEx uintptr
)

func init() {
	var err error
	kernel32, err = syscall.LoadLibrary("Kernel32.dll")
	if err != nil {
		panic("Unable to load Kernel32.dll")
	}
	pGetDiskFreeSpaceEx, err = syscall.GetProcAddress(kernel32, "GetDiskFreeSpaceExW")
	if err != nil {
		panic("Unable to get GetDiskFreeSpaceExW process")
	}
}

// disk usage of path/disk
func DiskUsage(path string) (*DiskStatus, error) {
	lpDirectoryName, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	lpFreeBytesAvailable := int64(0)
	lpTotalNumberOfBytes := int64(0)
	lpTotalNumberOfFreeBytes := int64(0)
	_, _, e := syscall.Syscall6(pGetDiskFreeSpaceEx, 4,
		uintptr(unsafe.Pointer(lpDirectoryName)),
		uintptr(unsafe.Pointer(&lpFreeBytesAvailable)),
		uintptr(unsafe.Pointer(&lpTotalNumberOfBytes)),
		uintptr(unsafe.Pointer(&lpTotalNumberOfFreeBytes)), 0, 0)
	if e != 0 {
		return nil, errors.New(e.Error())
	}
	status := &DiskStatus{
		All:  lpTotalNumberOfBytes,
		Free: lpFreeBytesAvailable,
	}
	status.Used = status.All - status.Free
	return status, nil
}
