//go:build !windows

package diag

func processMemory() (private, workingSet, privateWorkingSet uint64) { return 0, 0, 0 }
