package diag

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	getProcessMemoryInfo = syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
	queryWorkingSet      = syscall.NewLazyDLL("psapi.dll").NewProc("QueryWorkingSet")
)

type processMemoryCountersEX struct {
	Size, Faults                                                                          uint32
	PeakWS, WS, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile, Private uintptr
}

type processMemoryCountersEX2 struct {
	Size, Faults                                                                                     uint32
	PeakWS, WS, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile, Private, PrivateWS uintptr
	SharedCommit                                                                                     uint64
}

type workingSetExInformation struct {
	VirtualAddress uintptr
	Flags          uintptr
}

const (
	workingSetValid     = uintptr(1) << 0
	workingSetShared    = uintptr(1) << 15
	workingSetLargePage = uintptr(1) << 23
)

func processMemory() (private, workingSet, privateWorkingSet uint64) {
	var ex2 processMemoryCountersEX2
	ex2.Size = uint32(unsafe.Sizeof(ex2))
	if ok, _, _ := getProcessMemoryInfo.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&ex2)),
		uintptr(ex2.Size),
	); ok != 0 {
		private, workingSet, privateWorkingSet = uint64(ex2.Private), uint64(ex2.WS), uint64(ex2.PrivateWS)
	}

	// Windows 10 21H2 and earlier do not know PROCESS_MEMORY_COUNTERS_EX2.
	if workingSet == 0 {
		var ex processMemoryCountersEX
		ex.Size = uint32(unsafe.Sizeof(ex))
		if ok, _, _ := getProcessMemoryInfo.Call(
			uintptr(windows.CurrentProcess()),
			uintptr(unsafe.Pointer(&ex)),
			uintptr(ex.Size),
		); ok != 0 {
			private, workingSet = uint64(ex.Private), uint64(ex.WS)
		}
	}
	if privateWorkingSet == 0 {
		privateWorkingSet = privateWorkingSetFromPages()
	}
	return private, workingSet, privateWorkingSet
}

// privateWorkingSetFromPages is the fallback for systems without EX2. It walks
// the current working set once and counts resident pages that are not shared.
func privateWorkingSetFromPages() uint64 {
	handle := uintptr(windows.CurrentProcess())
	head := struct {
		Entries uintptr
		First   uintptr
	}{}
	ok, _, callErr := queryWorkingSet.Call(handle, uintptr(unsafe.Pointer(&head)), unsafe.Sizeof(head))
	if ok == 0 && callErr != windows.ERROR_BAD_LENGTH {
		return 0
	}

	count := head.Entries
	var buffer []uintptr
	for range 8 {
		if count == 0 || count > 1<<24 {
			return 0
		}
		capacity := count + count/8 + 1024
		buffer = make([]uintptr, capacity+1)
		ok, _, callErr = queryWorkingSet.Call(
			handle,
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(len(buffer))*unsafe.Sizeof(buffer[0]),
		)
		if ok != 0 {
			break
		}
		if callErr != windows.ERROR_BAD_LENGTH {
			return 0
		}
		// The working set can grow between the size query and the read.
		count = buffer[0]
		buffer = nil
	}
	if ok == 0 || len(buffer) < 2 || buffer[0] == 0 || buffer[0] >= uintptr(len(buffer)) {
		return 0
	}

	count = buffer[0]
	pages := make([]workingSetExInformation, count)
	for i := range pages {
		pages[i].VirtualAddress = buffer[i+1]
	}
	err := windows.QueryWorkingSetEx(
		windows.CurrentProcess(),
		uintptr(unsafe.Pointer(&pages[0])),
		uint32(uintptr(len(pages))*unsafe.Sizeof(pages[0])),
	)
	if err != nil {
		return 0
	}

	pageSize := uint64(os.Getpagesize())
	largePageSize := uint64(windows.GetLargePageMinimum())
	var total uint64
	for _, page := range pages {
		total += privateWorkingSetPageBytes(page.Flags, pageSize, largePageSize)
	}
	return total
}

func privateWorkingSetPageBytes(flags uintptr, pageSize, largePageSize uint64) uint64 {
	if flags&workingSetValid == 0 || flags&workingSetShared != 0 {
		return 0
	}
	if flags&workingSetLargePage != 0 && largePageSize > 0 {
		return largePageSize
	}
	return pageSize
}
