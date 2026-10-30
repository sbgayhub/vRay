package geo

import (
	"os"
	"testing"
	"time"
)

// TestDownloadRealFiles 会访问真实发布方一次，验证文件完整落盘。使用
// VRAY_REAL_GEO=1 运行；它会通过网络传输 26 MB。
func TestDownloadRealFiles(t *testing.T) {
	if os.Getenv("VRAY_REAL_GEO") != "1" {
		t.Skip("set VRAY_REAL_GEO=1 to download the real data files")
	}
	dir := os.Getenv("VRAY_GEODATA")
	if dir == "" {
		dir = t.TempDir()
	}
	d := New(dir)
	d.Start()
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		s := d.Status()
		if s.State == "ready" || s.State == "failed" {
			t.Logf("final: %+v", s)
			if s.State == "failed" {
				t.Fatalf("download failed: %s", s.Err)
			}
			if !d.Present() {
				t.Fatal("ready but files are missing")
			}
			for _, name := range files {
				info, err := os.Stat(dir + string(os.PathSeparator) + name)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				t.Logf("%s is %d bytes", name, info.Size())
			}
			return
		}
		time.Sleep(time.Second)
	}
	d.Cancel()
	t.Fatal("timed out")
}
