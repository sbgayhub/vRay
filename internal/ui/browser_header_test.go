package ui

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestBrowserSearchAndFiltersStayInPageHeader(t *testing.T) {
	for _, width := range []int{760, 960, 1120, 1440} {
		for _, page := range []Page{PageNodes, PageConnections} {
			t.Run(fmt.Sprintf("%d/%d", page, width), func(t *testing.T) {
				r, _ := newRoot(t)
				r.cur, r.route = page, pageRoutes[page].ID
				tr := ui.NewTester(r.View, width, 520)
				tr.Frame()
				header, ok := tr.Find("页面标题栏")
				if !ok || header.Y < captionHeight {
					t.Fatalf("missing page header: %+v", header)
				}
				labels := []string{"搜索节点", "延迟测速", "下载测速"}
				if page == PageConnections {
					labels = []string{"搜索连接", "全部", "TCP", "UDP"}
				}
				for _, label := range labels {
					bounds, found := tr.Find(label)
					if !found || bounds.W < 20 || bounds.X < header.X ||
						bounds.X+bounds.W > header.X+header.W+1 || bounds.Y < header.Y ||
						bounds.Y+bounds.H > header.Y+header.H+1 {
						t.Fatalf("%s outside page header: %+v header=%+v", label, bounds, header)
					}
				}
				if page == PageNodes {
					list, _ := tr.Find("节点列表")
					if list.Y < header.Y+header.H || list.Y+list.H > 520 || list.H < 200 {
						t.Fatalf("node list did not use remaining space: %+v", list)
					}
				}
			})
		}
	}
}
