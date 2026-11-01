package diag

import "testing"

func TestPrivateWorkingSetPageBytes(t *testing.T) {
	const pageSize, largePageSize = 4096, 2 << 20
	for _, tc := range []struct {
		name  string
		flags uintptr
		want  uint64
	}{
		{"private", workingSetValid, pageSize},
		{"shared", workingSetValid | workingSetShared, 0},
		{"invalid", 0, 0},
		{"private large page", workingSetValid | workingSetLargePage, largePageSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := privateWorkingSetPageBytes(tc.flags, pageSize, largePageSize); got != tc.want {
				t.Fatalf("got %d bytes, want %d", got, tc.want)
			}
		})
	}
}

func TestPrivateWorkingSetFallbackWalksResidentPages(t *testing.T) {
	if got := privateWorkingSetFromPages(); got == 0 {
		t.Fatal("working-set page fallback returned no private memory")
	}
}
