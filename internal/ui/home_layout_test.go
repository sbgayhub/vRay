package ui

import (
	"context"
	"fmt"
	"image/png"
	"math"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSidebarCollapsesNavigatesAndExpands(t *testing.T) {
	r, _ := newRoot(t)
	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	before, _ := tr.Find("侧边导航栏")
	if err := tr.Click("折叠导航栏"); err != nil {
		t.Fatal(err)
	}
	tr.Advance(time.Second)
	after, _ := tr.Find("侧边导航栏")
	if !r.navCollapsed || before.W-after.W < 100 || after.W != 64 {
		t.Fatalf("sidebar did not collapse: before=%+v after=%+v", before, after)
	}
	for i, route := range pageRoutes {
		button, found := tr.Find(route.Label)
		if !found || button.X+button.W > after.X+after.W {
			t.Fatalf("collapsed destination %s is missing", route.Label)
		}
		if err := tr.Click(route.Label); err != nil {
			t.Fatal(err)
		}
		tr.Frame()
		if r.cur != Page(i) || !r.navCollapsed {
			t.Fatalf("collapsed navigation failed for %s", route.Label)
		}
	}
	if err := tr.Click("展开导航栏"); err != nil {
		t.Fatal(err)
	}
	tr.Advance(time.Second)
	restored, _ := tr.Find("侧边导航栏")
	if r.navCollapsed || restored.W != before.W || r.cur != Page(len(pageRoutes)-1) {
		t.Fatal("expanding did not restore the sidebar and current page")
	}
}

func TestHomeColumnsAlignWithExpandedAndCollapsedNavigation(t *testing.T) {
	for _, width := range []int{760, 960, 1050, 1120, 1441} {
		for _, collapsed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/collapsed=%t", width, collapsed), func(t *testing.T) {
				r, _ := homeDashboardFixture(t)
				r.navCollapsed = collapsed
				tr := ui.NewTester(r.View, width, 1040)
				tr.Frame()
				get := func(label string) ui.Rect {
					b, ok := tr.Find(label)
					if !ok || b.W == 0 {
						t.Fatalf("missing card %s", label)
					}
					return b
				}
				left, right := get("首页出站方式"), get("首页代理方式")
				status, upload, download := get("首页连接状态"), get("首页上传速度"), get("首页下载速度")
				local, source := get("首页本地代理"), get("首页订阅概览")
				quality, memory := get("首页节点质量"), get("首页运行内存")
				closeTo := func(a, b float32) {
					if math.Abs(float64(a-b)) > 1 {
						t.Fatalf("column edges differ: %.2f vs %.2f", a, b)
					}
				}
				closeTo(status.X, left.X)
				closeTo(download.X+download.W, right.X+right.W)
				if float32(width)-r.sidebarWidth()-48 >= 830 {
					closeTo(status.X+status.W, left.X+left.W)
					closeTo(upload.X, right.X)
					closeTo(upload.X, quality.X)
					closeTo(upload.X+upload.W, quality.X+quality.W)
					closeTo(download.X, memory.X)
					closeTo(download.W, memory.W)
					closeTo(source.X+source.W, left.X+left.W)
				} else {
					closeTo(status.X+status.W, right.X+right.W)
					closeTo(upload.X, local.X)
					closeTo(upload.W, local.W)
					closeTo(download.X, source.X)
					closeTo(download.W, source.W)
				}
				closeTo(local.H, source.H)
				closeTo(source.H, quality.H)
				closeTo(quality.H, memory.H)
				if dir := os.Getenv("VRAY_HOME_PREVIEW"); dir != "" && collapsed && width == 1120 {
					tr.SetSize(1120, 760)
					tr.Advance(time.Second)
					f, err := os.Create(filepath.Join(dir, "dark-1120-collapsed.png"))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, tr.Image()); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestHomeActionsShareHeaderStyleAndCopyWorks(t *testing.T) {
	for _, width := range []int{760, 1120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			r, _ := homeDashboardFixture(t)
			tr := ui.NewTester(r.View, width, 760)
			tr.Frame()
			if width == 760 {
				tr.Scroll(600, 400, 0, 430)
				tr.Frame()
			}
			for _, item := range []struct{ title, card, action, footer string }{
				{"本地代理", "首页本地代理", "复制地址", "已运行 · 系统代理"},
				{"当前订阅", "首页订阅概览", "更新当前订阅", "30% 已用 · 89.00 GB / 300.00 GB"},
				{"节点质量", "首页节点质量", "检测节点", "下载 12.00 MB/s"},
				{"运行内存", "首页运行内存", "释放内存", "48 个协程"},
			} {
				header, found := tr.Find(item.title + "卡片标题")
				button, ok := tr.Find(item.action)
				if !found || !ok || button.W != 22 || button.H != 22 || button.Y < header.Y ||
					button.Y+button.H > header.Y+header.H+1 || button.X+button.W > header.X+header.W {
					t.Fatalf("%s is not a matching header action: %+v header=%+v", item.action, button, header)
				}
				card, _ := tr.Find(item.card)
				footer, found := tr.Find(item.footer)
				if !found || footer.Y+footer.H > card.Y+card.H || card.Y+card.H-footer.Y-footer.H > 20 {
					t.Fatalf("%s footer leaves excess space: %+v card=%+v", item.title, footer, card)
				}
			}
			if err := tr.Click("复制地址"); err != nil {
				t.Fatal(err)
			}
			tr.Frame()
			if got := tr.Clipboard(); got != "127.0.0.1:2080" || r.cur != PageHome || !tr.HasText("已复制") {
				t.Fatalf("copy action failed or opened settings: %q", got)
			}
		})
	}
}

func TestHomeHeaderProbeButtonTargetsCurrentNode(t *testing.T) {
	r, svc := newRoot(t)
	dialed := make(chan string, 1)
	p := svc.Prober()
	p.Dial = func(_ context.Context, address string) (net.Conn, error) {
		dialed <- address
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	}
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	if err := tr.Click("检测节点"); err != nil {
		t.Fatal(err)
	}
	select {
	case address := <-dialed:
		if address != "jp.example.com:443" {
			t.Fatalf("wrong node probed: %s", address)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("header action did not start a probe")
	}
	p.Wait()
	if r.cur != PageHome {
		t.Fatal("probe action navigated away from the dashboard")
	}
}
