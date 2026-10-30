package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/paths"
	"github.com/sbgayhub/vray/internal/store"
)

type slowPolicyTelemetry struct {
	app.FakeEngine
	gate  <-chan struct{}
	calls atomic.Int32
}

func (engine *slowPolicyTelemetry) PolicyHealth() map[string]xcore.PolicyHealth {
	engine.calls.Add(1)
	<-engine.gate
	return map[string]xcore.PolicyHealth{"p1": {Alive: true, Delay: 17, CheckedAt: time.Now()}}
}

func TestPolicyTelemetryNeverBlocksFramesOrSurvivesDisconnect(t *testing.T) {
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	defer release()
	engine := &slowPolicyTelemetry{gate: gate}
	state := app.NewState(app.Snapshot{
		Phase: app.PhaseConnected, Since: time.Now(),
		Settings: app.SettingsView{ActiveGroup: "g"},
		Groups:   []store.PolicyGroup{{ID: "g", Name: "Main", Type: "select", Selected: "p1", Members: []string{"p1"}}},
		Profiles: []app.ProfileView{{ID: "p1", Name: "First", Protocol: "vless"}},
	})
	p := paths.Paths{Root: t.TempDir()}
	svc := app.New(state, store.NewFile(p.DB()), app.NewMemSecrets(), engine, p)
	r := NewRoot(svc)
	r.cur, r.route = PageGroups, pageRoutes[PageGroups].ID
	tester := ui.NewTester(r.View, 1120, 760)
	frameDone := make(chan struct{})
	go func() { tester.Frame(); close(frameDone) }()
	select {
	case <-frameDone:
	case <-time.After(time.Second):
		t.Fatal("a frame waited for core telemetry")
	}
	tester.Advance(5 * time.Second)
	if engine.calls.Load() > 1 {
		t.Fatal("telemetry reads were not coalesced")
	}
	state.Update(func(sn *app.Snapshot) { sn.Phase = app.PhaseIdle })
	release()
	deadline := time.Now().Add(time.Second)
	for r.groups.pending != nil && time.Now().Before(deadline) {
		tester.Frame()
		time.Sleep(time.Millisecond)
	}
	if r.groups.pending != nil || engine.calls.Load() != 1 {
		t.Fatal("pending telemetry did not finish cleanly")
	}
	if len(r.groups.health) != 0 {
		t.Fatal("disconnected UI kept old health information")
	}
}

func policyFixture(t *testing.T, count int) (*Root, *app.Service) {
	t.Helper()
	r, svc := newRoot(t)
	svc.State().Update(func(sn *app.Snapshot) {
		sn.Phase = app.PhaseConnected
		sn.Since = time.Now().Add(-12 * time.Minute)
		sn.Settings.Theme = "dark"
		sn.Settings.ActiveGroup = "main"
		sn.Sources = []app.SourceView{{ID: "source", Name: "主力订阅"}, {ID: "backup", Name: "备用订阅"}}
		sn.Profiles = []app.ProfileView{}
		members := []string{}
		for i := range count {
			id := fmt.Sprintf("node-%d", i)
			protocol := []string{"vless", "hysteria2", "trojan"}[i%3]
			sn.Profiles = append(sn.Profiles, app.ProfileView{
				ID: id, Name: fmt.Sprintf("日本 · 东京 %02d", i+1), Protocol: protocol,
				SourceID: "source", SourceName: "主力订阅", Address: "fixture.invalid",
			})
			members = append(members, id)
		}
		sn.Groups = []store.PolicyGroup{
			{ID: "main", Name: "代理出口", Type: "select", SourceID: "source", Selected: "auto",
				Members: append([]string{"auto", "failover", "balance"}, members...)},
			{ID: "auto", Name: "自动选择", Type: "url-test", SourceID: "source", Members: members},
			{ID: "failover", Name: "故障转移", Type: "fallback", SourceID: "source", Members: members},
			{ID: "balance", Name: "负载均衡", Type: "load-balance", SourceID: "source", Members: members},
		}
		for i, name := range []string{"流媒体", "游戏加速", "香港地区", "日本地区", "新加坡地区", "美国地区", "下载专线", "备用线路"} {
			sn.Groups = append(sn.Groups, store.PolicyGroup{
				ID: fmt.Sprintf("region-%d", i), Name: name, Type: "select", SourceID: "backup", Members: members,
			})
		}
	})
	snap := svc.State().Snapshot()
	r.groups.healthSince, r.groups.healthGroup = snap.Since, snap.Settings.ActiveGroup
	r.groups.nextHealth = time.Now().Add(time.Hour)
	r.groups.health = map[string]xcore.PolicyHealth{}
	for i := range count {
		r.groups.health[fmt.Sprintf("node-%d", i)] = xcore.PolicyHealth{
			Alive: i%7 != 6, Delay: int64(23 + i*3), CheckedAt: time.Now(),
		}
	}
	r.cur, r.route = PageGroups, pageRoutes[PageGroups].ID
	return r, svc
}

func TestPolicyBrowserFillsAvailableHeightAndPreview(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		for _, size := range [][2]int{{1120, 760}, {760, 520}} {
			for _, language := range []string{"zh-Hans", "en"} {
				t.Run(fmt.Sprintf("%s-%s-%d", theme, language, size[0]), func(t *testing.T) {
					r, svc := policyFixture(t, 31)
					svc.State().Update(func(sn *app.Snapshot) { sn.Settings.Theme = theme; sn.Settings.Language = language })
					tester := ui.NewTester(r.View, size[0], size[1])
					tester.Frame()
					tester.Advance(time.Second)
					left, ok := tester.Find("策略组列表")
					if !ok {
						t.Fatal("missing group list")
					}
					right, ok := tester.Find("策略组成员")
					if !ok {
						t.Fatal("missing member list")
					}
					for _, bounds := range []ui.Rect{left, right} {
						if bounds.H < 100 || bounds.Y+bounds.H > float32(size[1])-20 || bounds.X+bounds.W > float32(size[0])-20 {
							t.Fatalf("list is clipped or too short: %+v", bounds)
						}
					}
					if float32(size[1])-right.Y-right.H > 70 {
						t.Fatalf("members leave unused window height: %+v", right)
					}
					if r.selectedGroup != "main" {
						t.Fatal("initial group should follow the active default")
					}
					if dir := os.Getenv("VRAY_POLICY_PREVIEW"); dir != "" {
						if err := os.MkdirAll(dir, 0755); err != nil {
							t.Fatal(err)
						}
						path := filepath.Join(dir, fmt.Sprintf("%s-%s-%d.png", theme, language, size[0]))
						f, err := os.Create(path)
						if err != nil {
							t.Fatal(err)
						}
						err = png.Encode(f, tester.Image())
						_ = f.Close()
						if err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func TestPolicyBrowserNestedNavigationSearchAndDisplaySort(t *testing.T) {
	r, svc := policyFixture(t, 31)
	svc.State().Update(func(sn *app.Snapshot) {
		sn.Groups[1].Type = "select"
		sn.Groups[1].Members = []string{"failover"}
		sn.Groups[1].Selected = "failover"
	})
	tester := ui.NewTester(r.View, 1120, 760)
	tester.Frame()
	if err := tester.Click("打开分组 自动选择"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if r.selectedGroup != "auto" || len(r.groups.trail) != 1 {
		t.Fatal("nested group was not opened")
	}
	if err := tester.Click("打开分组 故障转移"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if r.selectedGroup != "failover" || len(r.groups.trail) != 2 {
		t.Fatal("second nested group was not opened")
	}
	if err := tester.Click("返回上级分组"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if r.selectedGroup != "auto" || len(r.groups.trail) != 1 {
		t.Fatal("back lost the remaining ancestor")
	}
	if err := tester.Click("返回上级分组"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if r.selectedGroup != "main" || len(r.groups.trail) != 0 {
		t.Fatal("back did not restore the parent")
	}
	if err := tester.Click("搜索策略组成员"); err != nil {
		t.Fatal(err)
	}
	tester.Type("东京 31")
	tester.Frame()
	if _, ok := tester.Find("选择成员 日本 · 东京 31"); !ok {
		t.Fatal("member search did not reveal the target")
	}
	if _, ok := tester.Find("选择成员 日本 · 东京 01"); ok {
		t.Fatal("member search did not filter the list")
	}
	if err := tester.Click("清空搜索策略组成员"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	before := append([]string{}, svc.State().Snapshot().Groups[0].Members...)
	if err := tester.Click("按延迟排序"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	after := svc.State().Snapshot().Groups[0].Members
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatal("display sorting changed routing priority")
	}
	if err := tester.Click("筛选成员 groups"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if _, ok := tester.Find("选择成员 日本 · 东京 01"); ok {
		t.Fatal("group filter included nodes")
	}
	if err := tester.Click("筛选成员 nodes"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if _, ok := tester.Find("选择成员 自动选择"); ok {
		t.Fatal("node filter included groups")
	}
}

func TestPolicyBrowserVirtualizesLargeMembership(t *testing.T) {
	r, _ := policyFixture(t, 3000)
	tester := ui.NewTester(r.View, 1120, 760)
	tester.Frame()
	if _, ok := tester.Find("选择成员 日本 · 东京 3000"); ok {
		t.Fatal("off-screen members were eagerly built")
	}
	r.groups.members.ScrollTo(3002, ui.End)
	tester.Frame()
	tester.Advance(time.Second)
	if _, ok := tester.Find("选择成员 日本 · 东京 3000"); !ok {
		t.Fatal("virtual list cannot reach the last member")
	}
}
