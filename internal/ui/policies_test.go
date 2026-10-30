package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestPolicyPageSelectsAndActivatesImportedGroup(t *testing.T) {
	r, svc := newRoot(t)
	svc.ImportText(`proxies:
  - {name: First, type: vless, server: 127.0.0.1, port: 444, uuid: 11111111-2222-3333-4444-555555555555}
  - {name: Second, type: vless, server: 127.0.0.1, port: 445, uuid: 11111111-2222-3333-4444-555555555555}
proxy-groups:
  - {name: Main, type: select, proxies: [First, Second]}
`)
	deadline := time.Now().Add(3 * time.Second)
	for len(svc.State().Snapshot().Groups) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	snap := svc.State().Snapshot()
	if len(snap.Groups) != 1 {
		t.Fatal("import did not create a policy group")
	}
	g := snap.Groups[0]
	r.cur, r.route = PageGroups, pageRoutes[PageGroups].ID
	tester := ui.NewTester(r.View, 760, 520)
	tester.Frame()
	if err := tester.Click("选择成员 Second"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap = svc.State().Snapshot()
		if !snap.GroupBusy && snap.Groups[0].Selected == g.Members[1] {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if snap.Groups[0].Selected != g.Members[1] {
		t.Fatal("member selection did not reach the service")
	}
	tester.Frame()
	if err := tester.Click("使用策略组"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for svc.State().Snapshot().Settings.ActiveGroup != g.ID && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if svc.State().Snapshot().Settings.ActiveGroup != g.ID {
		t.Fatal("activation did not reach the service")
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	snap = svc.State().Snapshot()
	if snap.Settings.ActiveGroup != g.ID || snap.Groups[0].Selected != g.Members[1] {
		t.Fatal("UI policy choices were not persisted")
	}
}
