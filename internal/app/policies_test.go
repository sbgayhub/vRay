package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sbgayhub/vray/internal/store"
)

func connectedPolicyService(t *testing.T, active string) (*Service, *FakeEngine) {
	t.Helper()
	svc, engine := newTestService(t)
	for _, id := range []string{"p1", "p2", "p3"} {
		p := testProfile()
		p.ID = id
		if err := svc.AddTestProfile(p, "uuid"); err != nil {
			t.Fatal(err)
		}
	}
	d, err := svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	d.Groups = []store.PolicyGroup{
		{ID: "root", Type: "select", Selected: "left", Members: []string{"left", "right"}},
		{ID: "left", Type: "select", Selected: "p1", Members: []string{"p1", "p2", "p3"}},
		{ID: "right", Type: "select", Selected: "p2", Members: []string{"p1", "p2", "p3"}},
		{ID: "auto", Type: "url-test", Members: []string{"left", "right"}},
	}
	d.Settings.ActiveGroup = active
	if err := svc.store.Save(d); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	if err := svc.connectForTest("p1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.disconnectLocked() })
	return svc, engine
}

func TestPolicyChangesOnlyReconnectWhenEffectiveOutboundChanges(t *testing.T) {
	svc, engine := connectedPolicyService(t, "root")
	for _, operation := range []struct {
		group, member string
		activate      bool
	}{
		{group: "root", activate: true},
		{group: "left", member: "p1"},
		{group: "right", member: "p3"},
	} {
		awaitOperation(t, svc.SetPolicy(operation.group, operation.member, operation.activate))
	}
	if starts, stops := engine.Counts(); starts != 1 || stops != 0 {
		t.Fatalf("unchanged or inactive policies interrupted the connection: %d/%d", starts, stops)
	}
	awaitOperation(t, svc.SetPolicy("left", "p2", false))
	if starts, stops := engine.Counts(); starts != 2 || stops != 1 {
		t.Fatalf("selected nested group did not reconnect exactly once: %d/%d", starts, stops)
	}
	if snap := svc.state.Snapshot(); snap.Settings.ActiveGroup != "root" || snap.Phase != PhaseConnected {
		t.Fatal("changing a nested choice replaced the default group")
	}
	awaitOperation(t, svc.SetPolicy("right", "", true))
	awaitOperation(t, svc.SetPolicy("right", "", true))
	if starts, stops := engine.Counts(); starts != 3 || stops != 2 {
		t.Fatalf("re-selecting the same default interrupted the connection: %d/%d", starts, stops)
	}
}

func TestAutomaticPolicyDependsOnAllBranches(t *testing.T) {
	svc, engine := connectedPolicyService(t, "auto")
	awaitOperation(t, svc.SetPolicy("right", "p3", false))
	if starts, stops := engine.Counts(); starts != 2 || stops != 1 {
		t.Fatal("automatic group did not apply its nested member change")
	}
}

func TestPolicyChoicesInDirectModeDoNotReconnect(t *testing.T) {
	svc, engine := connectedPolicyService(t, "root")
	if err := svc.disconnectLocked(); err != nil {
		t.Fatal(err)
	}
	d, err := svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	d.Settings.RoutingMode = "direct"
	if err := svc.store.Save(d); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	if err := svc.connectForTest("p1"); err != nil {
		t.Fatal(err)
	}
	starts, stops := engine.Counts()
	awaitOperation(t, svc.SetPolicy("left", "p2", false))
	awaitOperation(t, svc.SetPolicy("right", "", true))
	if nextStarts, nextStops := engine.Counts(); nextStarts != starts || nextStops != stops {
		t.Fatal("saved policy choices interrupted a direct connection")
	}
}

func TestPolicyProbeDeduplicatesMembersAndSkipsUDP(t *testing.T) {
	svc, _ := newTestService(t)
	for _, id := range []string{"p1", "p2", "udp"} {
		p := testProfile()
		p.ID = id
		p.Address = id + ".invalid"
		if id == "udp" {
			p.Protocol = "hysteria2"
		}
		if err := svc.AddTestProfile(p, "secret"); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := svc.store.Load()
	d.Groups = []store.PolicyGroup{
		{ID: "root", Type: "select", Members: []string{"p1", "child", "udp"}},
		{ID: "child", Type: "select", Members: []string{"p1", "p2", "root"}},
	}
	if err := svc.store.Save(d); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	addresses := []string{}
	probe := svc.Prober()
	probe.Dial = func(_ context.Context, address string) (net.Conn, error) {
		addresses = append(addresses, address)
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	}
	awaitOperation(t, svc.TestPolicy("root"))
	probe.Wait()
	if len(addresses) != 2 || addresses[0] != "p1.invalid:443" || addresses[1] != "p2.invalid:443" {
		t.Fatalf("probe did not respect scope, deduplication or UDP: %v", addresses)
	}
	if svc.State().Snapshot().Settings.ActiveGroup != "" {
		t.Fatal("probing changed the active policy")
	}
}

func TestSubscriptionGroupsPersistSelectionsAndRemoveWithSource(t *testing.T) {
	const body = `proxies:
  - {name: A, type: vless, server: 127.0.0.1, port: 443, uuid: 11111111-2222-3333-4444-555555555555}
  - {name: B, type: vless, server: 127.0.0.1, port: 444, uuid: 11111111-2222-3333-4444-555555555555}
proxy-groups:
  - {name: Main, type: select, proxies: [A, B, DIRECT]}
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer server.Close()
	svc, _ := newTestService(t)
	before := svc.state.Snapshot().Version
	svc.Import(ImportInput{Text: server.URL})
	sn := waitImport(t, svc, before)
	if len(sn.Groups) != 1 || len(sn.Groups[0].Members) != 3 {
		t.Fatalf("groups not imported: %+v", sn.Groups)
	}
	g := sn.Groups[0]
	member := g.Members[1]
	awaitOperation(t, svc.SetPolicy(g.ID, member, true))
	if svc.state.Snapshot().Settings.ActiveGroup != g.ID {
		t.Fatal("default group was not persisted")
	}
	d, err := svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if d.Groups[0].Selected != member || d.Settings.ActiveGroup != g.ID {
		t.Fatal("saved selection missing")
	}
	awaitOperation(t, svc.RefreshSource(g.SourceID))
	sn = svc.state.Snapshot()
	if sn.Groups[0].ID != g.ID || sn.Groups[0].Selected != member {
		t.Fatal("refresh lost stable group/node references")
	}
	clone := sn
	clone.Groups[0].Members[0] = "corrupt"
	if svc.state.Snapshot().Groups[0].Members[0] == "corrupt" {
		t.Fatal("snapshot leaked group members")
	}
	awaitOperation(t, svc.DeleteSource(g.SourceID, false))
	d, _ = svc.store.Load()
	if len(d.Groups) != 0 || d.Settings.ActiveGroup != "" || len(d.Profiles) != 2 {
		t.Fatal("group cleanup damaged retained nodes")
	}
}

func TestPolicyCycleDoesNotHang(t *testing.T) {
	svc, _ := newTestService(t)
	d, _ := svc.store.Load()
	d.Groups = []store.PolicyGroup{{ID: "a", Type: "select", Members: []string{"b"}, Selected: "b"}, {ID: "b", Type: "select", Members: []string{"a"}, Selected: "a"}}
	d.Settings.ActiveGroup = "a"
	if err := svc.store.Save(d); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddTestProfile(testProfile(), "uuid"); err != nil {
		t.Fatal(err)
	}
	svc.Connect("p1")
	waitPhase(t, svc, PhaseFailed)
	if p := svc.state.Snapshot().Problem; p == nil || p.Code != "POLICY_INVALID" {
		t.Fatal("cycle was not rejected")
	}
}

func TestCustomPolicyGroupCreateEditDeleteAndReconnect(t *testing.T) {
	svc, engine := newTestService(t)
	for _, id := range []string{"p1", "p2", "p3"} {
		profile := testProfile()
		profile.ID = id
		profile.Name = id
		if err := svc.AddTestProfile(profile, "uuid"); err != nil {
			t.Fatal(err)
		}
	}

	awaitOperation(t, svc.SavePolicyGroup(PolicyGroupEdit{
		Name: "固定日本出口", Type: "url-test", URL: "https://example.com/generate_204",
		Interval: 120, Members: []string{"p1", "p2"},
	}))
	snap := svc.state.Snapshot()
	if len(snap.Groups) != 1 {
		t.Fatalf("custom group not created: %+v", snap.Groups)
	}
	group := snap.Groups[0]
	if group.ID == "" || group.SourceID == "" || group.Type != "url-test" ||
		group.Interval != 120 || group.URL != "https://example.com/generate_204" {
		t.Fatalf("custom group fields = %+v", group)
	}
	if !strings.HasPrefix(group.ID, "custom:") || !strings.HasPrefix(group.SourceID, "custom:") {
		t.Fatalf("custom group lacks a persistent namespace: %+v", group)
	}

	awaitOperation(t, svc.SavePolicyGroup(PolicyGroupEdit{
		ID: group.ID, Name: "日本容灾", Type: "fallback", Interval: 30,
		Members: []string{"p3", "p2", "p1"},
	}))
	snap = svc.state.Snapshot()
	if len(snap.Groups) != 1 || snap.Groups[0].Name != "日本容灾" ||
		snap.Groups[0].Type != "fallback" || fmt.Sprint(snap.Groups[0].Members) != "[p3 p2 p1]" {
		t.Fatalf("custom group edit lost order or type: %+v", snap.Groups)
	}

	awaitOperation(t, svc.SavePolicyGroup(PolicyGroupEdit{
		Name: "子策略", Type: "select", Members: []string{"p1"}, Selected: "p1",
	}))
	snap = svc.state.Snapshot()
	child := snap.Groups[1]
	if child.ID == "" || !strings.HasPrefix(child.ID, "custom:") {
		t.Fatalf("second custom group not created: %+v", snap.Groups)
	}

	d, err := svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := range d.Groups {
		if d.Groups[i].ID == group.ID {
			d.Groups[i].Members = []string{child.ID, "p2"}
		}
	}
	d.Settings.ActiveGroup = group.ID
	if err := svc.store.Save(d); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	if err := svc.connectForTest("p1"); err != nil {
		t.Fatal(err)
	}
	starts, stops := engine.Counts()
	awaitOperation(t, svc.DeletePolicyGroup(child.ID))
	snap = svc.state.Snapshot()
	if len(snap.Groups) != 1 || fmt.Sprint(snap.Groups[0].Members) != "[p2]" {
		t.Fatalf("delete did not remove nested references: %+v", snap.Groups)
	}
	if nextStarts, nextStops := engine.Counts(); nextStarts != starts+1 || nextStops != stops+1 {
		t.Fatalf("deleting an active-path group reconnect count = %d/%d, want %d/%d",
			nextStarts, nextStops, starts+1, stops+1)
	}
	if snap.Phase != PhaseConnected {
		t.Fatalf("connection did not recover after deleting a nested group: %+v", snap.Problem)
	}
}

func TestPolicyGroupEditorRejectsCyclesAndReadOnlyGroups(t *testing.T) {
	svc, _ := newTestService(t)
	profile := testProfile()
	profile.ID = "p1"
	if err := svc.AddTestProfile(profile, "uuid"); err != nil {
		t.Fatal(err)
	}
	awaitOperation(t, svc.SavePolicyGroup(PolicyGroupEdit{
		Name: "A", Type: "select", Members: []string{"p1"}, Selected: "p1",
	}))
	awaitOperation(t, svc.SavePolicyGroup(PolicyGroupEdit{
		Name: "B", Type: "select", Members: []string{"p1"}, Selected: "p1",
	}))
	snap := svc.state.Snapshot()
	a, b := snap.Groups[0], snap.Groups[1]
	awaitOperation(t, svc.SavePolicyGroup(PolicyGroupEdit{
		ID: a.ID, Name: "A", Type: "select", Members: []string{b.ID}, Selected: b.ID,
	}))
	awaitOperation(t, svc.SavePolicyGroup(PolicyGroupEdit{
		ID: b.ID, Name: "B", Type: "select", Members: []string{a.ID}, Selected: a.ID,
	}))
	if got := svc.state.Snapshot().GroupResult; !strings.Contains(got, "循环") {
		t.Fatalf("cycle error = %q", got)
	}

	d, err := svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	d.Groups = []store.PolicyGroup{{
		ID: "group:subscription", SourceID: "subscription", Name: "订阅组", Type: "select",
		Members: []string{"p1"}, Selected: "p1",
	}}
	if err := svc.store.Save(d); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	awaitOperation(t, svc.SavePolicyGroup(PolicyGroupEdit{
		ID: "group:subscription", Name: "改名", Type: "select",
		Members: []string{"p1"}, Selected: "p1",
	}))
	if got := svc.state.Snapshot().GroupResult; !strings.Contains(got, "订阅") {
		t.Fatalf("read-only group error = %q", got)
	}
	awaitOperation(t, svc.DeletePolicyGroup("group:subscription"))
	if got := svc.state.Snapshot().GroupResult; !strings.Contains(got, "订阅") {
		t.Fatalf("read-only delete error = %q", got)
	}
}

func TestCustomPolicyGroupsSurviveSubscriptionRefresh(t *testing.T) {
	const body = `proxies:
  - {name: A, type: vless, server: 127.0.0.1, port: 443, uuid: 11111111-2222-3333-4444-555555555555}
  - {name: B, type: vless, server: 127.0.0.1, port: 444, uuid: 11111111-2222-3333-4444-555555555555}
proxy-groups:
  - {name: Subscription group, type: select, proxies: [A, B]}
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	before := svc.state.Snapshot().Version
	svc.Import(ImportInput{Text: server.URL})
	snap := waitImport(t, svc, before)
	sourceID := snap.Groups[0].SourceID
	members := append([]string(nil), snap.Profiles[0].ID, snap.Profiles[1].ID)
	awaitOperation(t, svc.SavePolicyGroup(PolicyGroupEdit{
		Name: "Custom auto", Type: "url-test", Members: members,
	}))
	customID := ""
	for _, group := range svc.state.Snapshot().Groups {
		if strings.HasPrefix(group.ID, "custom:") {
			customID = group.ID
			break
		}
	}
	if customID == "" {
		t.Fatal("custom group was not created")
	}
	awaitOperation(t, svc.RefreshSource(sourceID))
	var found bool
	for _, group := range svc.state.Snapshot().Groups {
		if group.ID == customID && group.Name == "Custom auto" && fmt.Sprint(group.Members) == fmt.Sprint(members) {
			found = true
		}
	}
	if !found {
		t.Fatalf("subscription refresh overwrote the custom group: %+v", svc.state.Snapshot().Groups)
	}
}
