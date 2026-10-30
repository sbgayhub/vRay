package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

func TestDownloadSpeedUIShowsSavedResultAndManualControls(t *testing.T) {
	for _, width := range []int{760, 1120} {
		for _, theme := range []string{"light", "dark"} {
			t.Run(fmt.Sprintf("%d/%s", width, theme), func(t *testing.T) {
				r, svc := newRoot(t)
				r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
				svc.State().Update(func(sn *app.Snapshot) {
					sn.Settings.Theme = theme
					sn.NodeSpeeds = map[string]app.DownloadTest{"p1": {DownloadSpeed: store.DownloadSpeed{
						BytesPerSecond: 6553600, Bytes: 32 << 20, Duration: 5 * time.Second, CheckedAt: time.Now().UTC(),
					}}}
				})
				tr := ui.NewTester(r.View, width, 820)
				tr.Advance(time.Second)
				button, found := tr.Find("下载测速")
				if !found || button.Y < captionHeight || button.Y > 100 || button.X+button.W > float32(width) {
					t.Fatalf("download action is not in page heading: %+v", button)
				}
				if !tr.HasText("6.25 MB/s") || !tr.HasText("重新测试下载速度") || !tr.HasText("延迟测速") {
					t.Fatal("saved speed or manual measurement controls are missing")
				}
				if out := os.Getenv("VRAY_DOWNLOAD_PREVIEW"); out != "" {
					if err := os.MkdirAll(out, 0o755); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(out, fmt.Sprintf("%s-%d.png", theme, width)))
					if err != nil {
						t.Fatal(err)
					}
					if err := png.Encode(file, tr.Image()); err != nil {
						file.Close()
						t.Fatal(err)
					}
					file.Close()
				}
				// 模拟引擎无法打开隧道，但实际按钮必须到达 worker，
				// 并在失败时保留已保存的速度。
				if err := tr.Click("下载测速"); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(2 * time.Second)
				for svc.State().Snapshot().NodeSpeeds["p1"].Running && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				result := svc.State().Snapshot().NodeSpeeds["p1"]
				if result.Running || result.Error == "" || result.BytesPerSecond != 6553600 {
					t.Fatalf("manual action/failure handling failed: %+v", result)
				}
			})
		}
	}
}
