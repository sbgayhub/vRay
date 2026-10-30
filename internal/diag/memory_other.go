//go:build !windows

package diag

func processMemory() (private, workingSet uint64) { return 0, 0 }
