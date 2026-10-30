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
	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/paths"
	"github.com/sbgayhub/vray/internal/store"
)

type connectionTestEngine struct {
	app.FakeEngine
	list []xcore.Connection
}

func (e *connectionTestEngine) Connections() []xcore.Connection {
	return append([]xcore.Connection(nil), e.list...)
}

func newConnectionsRoot(t *testing.T) (*Root, *connectionTestEngine) {
	t.Helper()
	e := &connectionTestEngine{}
	p := paths.Paths{Root: t.TempDir()}
	svc := app.New(app.NewState(app.Snapshot{Phase: app.PhaseConnected, Active: "p1", Profiles: []app.ProfileView{{ID: "p1", Name: "东京 VLESS", Protocol: "vless"}}}), store.NewFile(p.DB()), app.NewMemSecrets(), e, p)
	r := NewRoot(svc)
	r.cur, r.route = PageConnections, "connections"
	return r, e
}

func connectionFixture() xcore.Connection {
	return xcore.Connection{ID: 1, Source: "127.0.0.1:56890", Destination: "video.googlevideo.com:443", Network: "tcp", Protocol: "tls", Inbound: "in-socks", Outbound: "proxy", Started: time.Now().Add(-65 * time.Second), Upload: 15 << 10, Download: 38 << 20}
}

func TestConnectionsPageRefreshesSelectsAndRemoves(t *testing.T) {
	r, e := newConnectionsRoot(t)
	e.list = []xcore.Connection{connectionFixture()}
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	if !tr.HasText("实时连接") || !tr.HasText("video.googlevideo.com:443") || !tr.HasText("东京 VLESS") {
		t.Fatal("live connection missing")
	}
	if err := tr.Click("查看连接 1"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if !tr.HasText("127.0.0.1:56890") || !tr.HasText("VLESS") || !tr.HasText("本地代理") {
		t.Fatal("detail fields missing")
	}
	e.list = nil
	tr.Advance(time.Second)
	if !tr.HasText("暂无活动连接") || tr.HasText("video.googlevideo.com:443") {
		t.Fatal("finished connection stayed visible")
	}
}

func TestConnectionsSearchAndNetworkFilters(t *testing.T) {
	r, e := newConnectionsRoot(t)
	udp := connectionFixture()
	udp.ID = 2
	udp.Network = "udp"
	udp.Protocol = "quic"
	udp.Outbound = "direct"
	udp.Destination = "dns.example.com:53"
	e.list = []xcore.Connection{connectionFixture(), udp}
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	if err := tr.Click("UDP"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if tr.HasText("video.googlevideo.com:443") || !tr.HasText("dns.example.com:53") {
		t.Fatal("UDP filter incorrect")
	}
	if err := tr.Click("全部"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if err := tr.Click("搜索连接"); err != nil {
		t.Fatal(err)
	}
	tr.Type("东京")
	tr.Frame()
	if !tr.HasText("video.googlevideo.com:443") || tr.HasText("dns.example.com:53") {
		t.Fatal("search did not match node name")
	}
	for query, want := range map[string]int{"VIDEO": 1, "56890": 2, "tls": 1, "东京": 1} {
		if got := filterConnections(e.list, query, "", r.svc.State().Snapshot()); len(got) != want {
			t.Fatalf("query %q: %+v", query, got)
		}
	}
	tr.Key(0, ui.KeyEscape)
	tr.Frame()
	if r.connections.query != "" || !tr.HasText("dns.example.com:53") {
		t.Fatal("CrychicUI SearchInput Escape did not clear the query")
	}
}

func TestConnectionKeepsActualNodeAndDropsPageCache(t *testing.T) {
	r, e := newConnectionsRoot(t)
	row := connectionFixture()
	row.NodeName, row.NodeProtocol = "实际使用的香港节点", "vless"
	if len(filterConnections([]xcore.Connection{row}, "VLESS", "", r.svc.State().Snapshot())) != 1 {
		t.Fatal("node protocol search did not match")
	}
	e.list = []xcore.Connection{row}
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	if !tr.HasText(row.NodeName) || tr.HasText("东京 VLESS") {
		t.Fatal("selected profile replaced the actual connection node")
	}
	r.cur, r.route = PageHome, "home"
	tr.Frame()
	if r.connections.rows != nil || r.connections.filtered != nil {
		t.Fatal("inactive page retained destination snapshots")
	}
}

func TestConnectionListVirtualizesAndFitsThemes(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		for _, size := range [][2]int{{760, 520}, {1120, 760}} {
			t.Run(fmt.Sprintf("%s-%d", theme, size[0]), func(t *testing.T) {
				r, e := newConnectionsRoot(t)
				for i := range 2000 {
					row := connectionFixture()
					row.ID = uint64(i + 1)
					row.Destination = fmt.Sprintf("video-%d.googlevideo.com:443", i)
					e.list = append(e.list, row)
				}
				r.connections.selected = 1
				r.svc.State().Update(func(sn *app.Snapshot) { sn.Settings.Theme = theme })
				tr := ui.NewTester(r.View, size[0], size[1])
				tr.Frame()
				tr.Advance(time.Second)
				if tr.HasText("video-1999.googlevideo.com:443") {
					t.Fatal("offscreen row rendered")
				}
				bounds, ok := tr.Find("活动连接列表")
				if !ok || bounds.Y+bounds.H > float32(size[1])+1 || bounds.H < 160 {
					t.Fatalf("list outside window: %+v", bounds)
				}
				if dir := os.Getenv("VRAY_CONNECTION_PREVIEW"); dir != "" {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-%d.png", theme, size[0])))
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
