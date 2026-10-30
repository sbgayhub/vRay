package diag

import "runtime"

// MemoryStats 区分存活的 Go 对象、运行时驻留内存与原生进程计数。
// 读取它不会强制 GC，也不会裁剪工作集。
type MemoryStats struct {
	HeapAllocBytes    uint64  `json:"heapAllocBytes"`
	HeapInuseBytes    uint64  `json:"heapInuseBytes"`
	HeapIdleBytes     uint64  `json:"heapIdleBytes"`
	HeapReleasedBytes uint64  `json:"heapReleasedBytes"`
	RuntimeUsedBytes  uint64  `json:"runtimeUsedBytes"`
	TotalAllocBytes   uint64  `json:"totalAllocBytes"`
	GCCount           uint32  `json:"gcCount"`
	GCCPUFraction     float64 `json:"gcCPUFraction"`
	Goroutines        int     `json:"goroutines"`
	PrivateBytes      uint64  `json:"privateBytes,omitempty"`
	WorkingSetBytes   uint64  `json:"workingSetBytes,omitempty"`
}

func ReadMemory() MemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	private, workingSet := processMemory()
	return MemoryStats{
		HeapAllocBytes: m.HeapAlloc, HeapInuseBytes: m.HeapInuse,
		HeapIdleBytes: m.HeapIdle, HeapReleasedBytes: m.HeapReleased,
		RuntimeUsedBytes: m.Sys - m.HeapReleased, TotalAllocBytes: m.TotalAlloc,
		GCCount: m.NumGC, GCCPUFraction: m.GCCPUFraction,
		Goroutines: runtime.NumGoroutine(), PrivateBytes: private, WorkingSetBytes: workingSet,
	}
}
