package diag

import (
	"syscall"
	"unsafe"
)

var getProcessMemoryInfo = syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

func processMemory() (private, workingSet uint64) {
	var m struct {
		Size, Faults                                                                          uint32
		PeakWS, WS, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile, Private uintptr
	}
	m.Size = uint32(unsafe.Sizeof(m))
	ok, _, _ := getProcessMemoryInfo.Call(^uintptr(0), uintptr(unsafe.Pointer(&m)), uintptr(m.Size))
	if ok == 0 {
		return 0, 0
	}
	return uint64(m.Private), uint64(m.WS)
}
