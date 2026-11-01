package diag

import (
	"runtime"
	"testing"
)

func TestReadMemoryReportsGoAndWindowsMemory(t *testing.T) {
	m := ReadMemory()
	if m.HeapAllocBytes == 0 || m.RuntimeUsedBytes < m.HeapAllocBytes || m.Goroutines == 0 {
		t.Fatalf("invalid runtime snapshot: %+v", m)
	}
	if runtime.GOOS == "windows" &&
		(m.PrivateBytes == 0 || m.WorkingSetBytes == 0 || m.PrivateWorkingSetBytes == 0) {
		t.Fatalf("missing Windows process counters: %+v", m)
	}
}
