package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/store"
)

// PolicyGroupEdit 是本地策略组完整的用户可编辑形态。
// 新组的稳定 ID 与 SourceID 由服务分配。
type PolicyGroupEdit struct {
	ID       string
	Name     string
	Type     string
	URL      string
	Interval int
	Members  []string
	Selected string
}

func (s *Service) PolicyHealth() map[string]xcore.PolicyHealth {
	if source, ok := s.engine.(interface {
		PolicyHealth() map[string]xcore.PolicyHealth
	}); ok {
		return source.PolicyHealth()
	}
	return map[string]xcore.PolicyHealth{}
}

func mergePolicyGroups(existing, incoming []store.PolicyGroup, sourceID string, nodes map[string]string) []store.PolicyGroup {
	result := []store.PolicyGroup{}
	old := map[string]store.PolicyGroup{}
	references := map[string]string{}
	for name, id := range nodes {
		references[name] = id
	}
	for _, g := range existing {
		if g.SourceID != sourceID {
			result = append(result, g)
		} else {
			old[g.ID] = g
		}
	}
	for _, g := range incoming {
		hash := sha256.Sum256([]byte(g.Name))
		references[g.Name] = "group:" + sourceID + ":" + hex.EncodeToString(hash[:8])
	}
	for _, g := range incoming {
		g.ID, g.SourceID = references[g.Name], sourceID
		members := []string{}
		for _, member := range g.Members {
			if id := references[member]; id != "" {
				members = append(members, id)
			} else if member == "DIRECT" || member == "REJECT" || member == "REJECT-DROP" {
				members = append(members, member)
			}
		}
		g.Members = members
		g.Selected = old[g.ID].Selected
		if !slices.Contains(members, g.Selected) {
			g.Selected = ""
			if len(members) > 0 {
				g.Selected = members[0]
			}
		}
		result = append(result, g)
	}
	return result
}

// SavePolicyGroup 创建或更新用户自有的分组。订阅分组与从分享链接捕获的
// 分组在这里保持只读，以便下次刷新可以更新它们，而不会悄悄丢弃本地编辑。
func (s *Service) SavePolicyGroup(edit PolicyGroupEdit) <-chan struct{} {
	done := make(chan struct{})
	started := false
	s.state.Update(func(sn *Snapshot) {
		if !sn.GroupBusy {
			sn.GroupBusy = true
			sn.GroupResult = ""
			started = true
		}
	})
	if !started {
		s.state.Update(func(sn *Snapshot) {
			sn.GroupResult = "另一个策略操作正在进行，请稍后重试"
		})
		close(done)
		return done
	}
	go func() {
		defer close(done)
		defer s.state.Update(func(sn *Snapshot) { sn.GroupBusy = false })
		s.mu.Lock()
		defer s.mu.Unlock()
		fail := func(message string) {
			s.state.Update(func(sn *Snapshot) { sn.GroupResult = message })
		}

		d, err := s.store.Load()
		if err != nil {
			fail("读取策略组失败")
			return
		}
		groupsBefore := clonePolicyGroups(d.Groups)
		groupID := strings.TrimSpace(edit.ID)
		name := strings.TrimSpace(edit.Name)
		if name == "" {
			fail("策略组名称不能为空")
			return
		}
		if len([]rune(name)) > 80 {
			fail("策略组名称不能超过 80 个字符")
			return
		}
		if !validPolicyGroupType(edit.Type) {
			fail("请选择有效的策略组类型")
			return
		}

		existingAt := -1
		sourceID := ""
		if groupID == "" {
			token := newID()
			groupID = "custom:" + token
			sourceID = "custom:" + token
		} else {
			for i := range d.Groups {
				if d.Groups[i].ID == groupID {
					existingAt = i
					break
				}
			}
			if existingAt < 0 {
				fail("策略组已不存在")
				return
			}
			existing := d.Groups[existingAt]
			if !policyGroupEditable(existing) {
				fail("订阅策略组由订阅更新维护，不能直接编辑")
				return
			}
			sourceID = existing.SourceID
		}

		for i, g := range d.Groups {
			if i != existingAt && g.Name == name {
				fail("已有同名策略组")
				return
			}
		}

		members := make([]string, 0, len(edit.Members))
		seen := make(map[string]bool, len(edit.Members))
		nodeIDs := make(map[string]bool, len(d.Profiles))
		groupIDs := make(map[string]bool, len(d.Groups))
		for _, profile := range d.Profiles {
			nodeIDs[profile.ID] = true
		}
		for _, group := range d.Groups {
			groupIDs[group.ID] = true
		}
		for _, member := range edit.Members {
			member = strings.TrimSpace(member)
			if member == "" || seen[member] {
				continue
			}
			if member == groupID || (!nodeIDs[member] && !groupIDs[member] && !policyBuiltinTarget(member)) {
				fail("策略组包含已移除或无效的成员")
				return
			}
			seen[member] = true
			members = append(members, member)
		}
		if len(members) > 256 {
			fail("策略组成员过多，最多支持 256 个")
			return
		}
		minimum := 1
		if edit.Type != "select" {
			minimum = 2
		}
		if len(members) < minimum {
			if minimum == 2 {
				fail("自动策略组至少需要两个成员")
			} else {
				fail("策略组至少需要一个成员")
			}
			return
		}

		selected := ""
		probeURL := ""
		interval := 0
		if edit.Type == "select" {
			selected = strings.TrimSpace(edit.Selected)
			if !seen[selected] {
				selected = members[0]
			}
		} else {
			probeURL = strings.TrimSpace(edit.URL)
			if probeURL != "" && !looksLikeURL(probeURL) {
				fail("探测地址必须是有效的 HTTP 或 HTTPS 地址")
				return
			}
			interval = edit.Interval
			if interval == 0 {
				interval = 300
			}
			if interval < 30 || interval > 86400 {
				fail("探测间隔需在 30–86400 秒之间")
				return
			}
		}

		next := store.PolicyGroup{
			ID: groupID, SourceID: sourceID, Name: name, Type: edit.Type,
			Members: members, Selected: selected, URL: probeURL, Interval: interval,
		}
		if existingAt >= 0 {
			d.Groups[existingAt] = next
		} else {
			d.Groups = append(d.Groups, next)
		}
		if policyGroupCycle(d.Groups) {
			fail("策略组存在循环引用")
			return
		}
		if err := s.store.Save(d); err != nil {
			fail("保存策略组失败")
			return
		}

		reconnect := policyDependsOn(groupsBefore, d.Settings.ActiveGroup, groupID) ||
			policyDependsOn(d.Groups, d.Settings.ActiveGroup, groupID)
		if err := s.Load(); err != nil {
			fail("重新读取策略组失败")
			return
		}
		if err := s.reconnectAfterPolicyChange(reconnect, d.Settings.RoutingMode); err != nil {
			fail(err.Error())
		}
	}()
	return done
}

// DeletePolicyGroup 只删除本地自有的分组，并清理所有指向它的稳定引用。
// 若该分组参与当前出站，连接会经由新图重建一次。
func (s *Service) DeletePolicyGroup(id string) <-chan struct{} {
	done := make(chan struct{})
	started := false
	s.state.Update(func(sn *Snapshot) {
		if !sn.GroupBusy {
			sn.GroupBusy = true
			sn.GroupResult = ""
			started = true
		}
	})
	if !started {
		s.state.Update(func(sn *Snapshot) {
			sn.GroupResult = "另一个策略操作正在进行，请稍后重试"
		})
		close(done)
		return done
	}
	go func() {
		defer close(done)
		defer s.state.Update(func(sn *Snapshot) { sn.GroupBusy = false })
		s.mu.Lock()
		defer s.mu.Unlock()
		fail := func(message string) {
			s.state.Update(func(sn *Snapshot) { sn.GroupResult = message })
		}

		d, err := s.store.Load()
		if err != nil {
			fail("读取策略组失败")
			return
		}
		index := -1
		for i := range d.Groups {
			if d.Groups[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			fail("策略组已不存在")
			return
		}
		if !policyGroupEditable(d.Groups[index]) {
			fail("订阅策略组由订阅更新维护，不能直接删除")
			return
		}

		groupsBefore := clonePolicyGroups(d.Groups)
		activeBefore := d.Settings.ActiveGroup
		d.Groups = append(d.Groups[:index], d.Groups[index+1:]...)
		for i := range d.Groups {
			g := &d.Groups[i]
			g.Members = slices.DeleteFunc(g.Members, func(member string) bool { return member == id })
			if g.Selected == id {
				g.Selected = ""
				if len(g.Members) > 0 {
					g.Selected = g.Members[0]
				}
			}
		}
		if activeBefore == id {
			d.Settings.ActiveGroup = ""
		}
		if policyGroupCycle(d.Groups) {
			fail("删除后策略组存在循环引用")
			return
		}
		if err := s.store.Save(d); err != nil {
			fail("删除策略组失败")
			return
		}

		reconnect := policyDependsOn(groupsBefore, activeBefore, id)
		if err := s.Load(); err != nil {
			fail("重新读取策略组失败")
			return
		}
		if err := s.reconnectAfterPolicyChange(reconnect, d.Settings.RoutingMode); err != nil {
			fail(err.Error())
		}
	}()
	return done
}

func (s *Service) reconnectAfterPolicyChange(changed bool, routingMode string) error {
	if !changed || routingMode == "direct" || s.state.Snapshot().Phase != PhaseConnected {
		return nil
	}
	active := s.state.Snapshot().Active
	if err := s.disconnectLocked(); err != nil {
		return err
	}
	s.state.SetPhase(PhaseConnecting, nil)
	return s.connectLocked(active)
}

func clonePolicyGroups(groups []store.PolicyGroup) []store.PolicyGroup {
	out := make([]store.PolicyGroup, len(groups))
	for i := range groups {
		out[i] = groups[i]
		out[i].Members = append([]string(nil), groups[i].Members...)
	}
	return out
}

func policyGroupEditable(g store.PolicyGroup) bool {
	return strings.HasPrefix(g.ID, "custom:") || strings.HasPrefix(g.SourceID, "local:")
}

func validPolicyGroupType(kind string) bool {
	switch kind {
	case "select", "url-test", "fallback", "load-balance":
		return true
	default:
		return false
	}
}

func policyBuiltinTarget(id string) bool {
	return id == "DIRECT" || id == "REJECT" || id == "REJECT-DROP"
}

func policyGroupCycle(groups []store.PolicyGroup) bool {
	byID := make(map[string]store.PolicyGroup, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
	}
	state := make(map[string]uint8, len(groups))
	var visit func(string) bool
	visit = func(id string) bool {
		if policyBuiltinTarget(id) {
			return false
		}
		if _, ok := byID[id]; !ok {
			return false
		}
		switch state[id] {
		case 1:
			return true
		case 2:
			return false
		}
		state[id] = 1
		for _, member := range byID[id].Members {
			if visit(member) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for id := range byID {
		if visit(id) {
			return true
		}
	}
	return false
}

// 只准备被选中的图。手动选择会显式失败；自动策略可以跳过不可达的候选，
// 而不会回退到直连。
func (s *Service) preparePolicyInput(ctx context.Context, in *configgen.Input, d store.Data) error {
	groups := map[string]store.PolicyGroup{}
	for _, g := range d.Groups {
		groups[g.ID] = g
		in.PolicyGroups = append(in.PolicyGroups, configgen.PolicyGroup{ID: g.ID, Name: g.Name, Type: g.Type, Members: g.Members, Selected: g.Selected})
	}
	nodes := map[string]store.Profile{}
	for _, p := range d.Profiles {
		nodes[p.ID] = p
	}
	needed := map[string]bool{}
	visiting := map[string]bool{}
	probeConfigured := false
	var collect func(string) error
	collect = func(id string) error {
		if id == "DIRECT" || id == "REJECT" || id == "REJECT-DROP" {
			return nil
		}
		if _, ok := nodes[id]; ok {
			needed[id] = true
			return nil
		}
		g, ok := groups[id]
		if !ok {
			return fmt.Errorf("策略组或成员已不存在")
		}
		if visiting[id] || len(visiting) > 64 {
			return fmt.Errorf("策略组存在循环引用")
		}
		visiting[id] = true
		defer delete(visiting, id)
		if g.Type == "select" {
			member := g.Selected
			if member == "" && len(g.Members) > 0 {
				member = g.Members[0]
			}
			return collect(member)
		}
		if !probeConfigured {
			probeConfigured = true
			if looksLikeURL(g.URL) {
				in.Settings.ProbeURL = g.URL
			}
			if in.Settings.ProbeURL == "" {
				in.Settings.ProbeURL = "https://www.gstatic.com/generate_204"
			}
			in.Settings.ProbeInterval = fmt.Sprintf("%ds", max(30, min(86400, g.Interval)))
		}
		for _, member := range g.Members {
			if err := collect(member); err != nil {
				return err
			}
		}
		return nil
	}
	if err := collect(d.Settings.ActiveGroup); err != nil {
		return err
	}
	if len(needed) > 256 {
		return fmt.Errorf("策略组成员过多，最多支持 256 个节点")
	}
	prepared := []configgen.PolicyNode{}
	var failures []string
	var mu sync.Mutex
	var workers sync.WaitGroup
	slots := make(chan struct{}, 6)
	record := func(name, reason string) {
		mu.Lock()
		failures = append(failures, name+"："+TrimDetail(reason))
		mu.Unlock()
	}
	for _, profile := range d.Profiles {
		if !needed[profile.ID] {
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			// 取证等待超时：该节点本次不参与。计入失败，避免组静默缩水。
			record(profile.Name, "等待节点取证超时")
			continue
		}
		profile := profile
		workers.Go(func() {
			defer func() { <-slots }()
			secret, err := s.secretOf(profile)
			if err != nil {
				record(profile.Name, err.Error())
				return
			}
			node := outboundOf(profile, secret)
			if provider, ok := s.engine.(interface {
				PrepareNode(context.Context, configgen.Outbound) (configgen.Outbound, error)
			}); ok {
				node, err = provider.PrepareNode(ctx, node)
			}
			if err != nil {
				record(profile.Name, err.Error())
				return
			}
			mu.Lock()
			prepared = append(prepared, configgen.PolicyNode{ID: profile.ID, Outbound: node})
			mu.Unlock()
		})
	}
	workers.Wait()
	if len(failures) > 0 {
		// 失败的节点会被剔除出策略组：必须留下线索，否则用户面对
		// “候选变少/组失效”无从排查。日志页是常驻的用户可见出口。
		slices.Sort(failures)
		slog.Warn("策略组部分节点取证失败，已从组中剔除", "失败数", len(failures), "明细", strings.Join(failures, "；"))
	}
	if len(prepared) == 0 && len(failures) > 0 {
		return fmt.Errorf("策略组的全部节点都无法使用：%s", failures[0])
	}
	slices.SortFunc(prepared, func(a, b configgen.PolicyNode) int { return strings.Compare(a.ID, b.ID) })
	in.PolicyNodes, in.ActivePolicy = prepared, d.Settings.ActiveGroup
	if len(prepared) > 0 {
		in.Active = prepared[0].Outbound
	} else {
		in.Active.AllowInsecure = false
	}
	return nil
}

// SetPolicy 保存默认分组或手动成员，并在同一把操作锁下应用到运行中的内核。
// 分组 ID 为空时回到单节点模式。
func (s *Service) SetPolicy(groupID, member string, activate bool) <-chan struct{} {
	done := make(chan struct{})
	started := false
	s.state.Update(func(sn *Snapshot) {
		if !sn.GroupBusy {
			sn.GroupBusy = true
			sn.GroupResult = ""
			started = true
		}
	})
	if !started {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		defer s.state.Update(func(sn *Snapshot) { sn.GroupBusy = false })
		s.mu.Lock()
		defer s.mu.Unlock()
		fail := func(err error) { s.state.Update(func(sn *Snapshot) { sn.GroupResult = err.Error() }) }
		d, err := s.store.Load()
		if err != nil {
			fail(err)
			return
		}
		found := groupID == ""
		changed := false
		reconnect := false
		for i := range d.Groups {
			g := &d.Groups[i]
			if g.ID != groupID {
				continue
			}
			found = true
			if member != "" {
				if g.Type != "select" || !slices.Contains(g.Members, member) {
					fail(fmt.Errorf("该策略组不支持选择此成员"))
					return
				}
				if g.Selected != member {
					previous := g.Selected
					if previous == "" && len(g.Members) > 0 {
						previous = g.Members[0]
					}
					reconnect = previous != member && policyDependsOn(d.Groups, d.Settings.ActiveGroup, groupID)
					g.Selected = member
					changed = true
				}
			}
		}
		if !found {
			fail(fmt.Errorf("策略组已不存在"))
			return
		}
		if activate && d.Settings.ActiveGroup != groupID {
			d.Settings.ActiveGroup = groupID
			changed, reconnect = true, true
		}
		if !changed {
			return
		}
		if err := s.store.Save(d); err != nil {
			fail(err)
			return
		}
		snap := s.state.Snapshot()
		if err := s.Load(); err != nil {
			fail(err)
			return
		}
		if snap.Phase == PhaseConnected && reconnect && d.Settings.RoutingMode != "direct" {
			if err := s.disconnectLocked(); err != nil {
				fail(err)
				return
			}
			s.state.SetPhase(PhaseConnecting, nil)
			if err := s.connectLocked(snap.Active); err != nil {
				fail(err)
			}
		}
	}()
	return done
}

// 手动分组中未选中的分支不会影响当前出站。自动分组依赖每个成员；
// visited 集合同时用于限制循环。
func policyDependsOn(groups []store.PolicyGroup, root, target string) bool {
	if root == "" {
		return false
	}
	byID := make(map[string]store.PolicyGroup, len(groups))
	for _, g := range groups {
		byID[g.ID] = g
	}
	seen := map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if id == target {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		g, ok := byID[id]
		if !ok {
			return false
		}
		if g.Type == "select" {
			selected := g.Selected
			if selected == "" && len(g.Members) > 0 {
				selected = g.Members[0]
			}
			return visit(selected)
		}
		for _, member := range g.Members {
			if visit(member) {
				return true
			}
		}
		return false
	}
	return visit(root)
}

// TestPolicy 探测本组（含嵌套分组）中去重后的 TCP 节点。
// 它复用现有的可取消探测器，且从不切换当前节点。
func (s *Service) TestPolicy(groupID string) <-chan struct{} {
	return s.runAsync(func() {
		d, err := s.store.Load()
		if err != nil {
			return
		}
		groups := make(map[string]store.PolicyGroup, len(d.Groups))
		nodes := make(map[string]store.Profile, len(d.Profiles))
		for _, g := range d.Groups {
			groups[g.ID] = g
		}
		for _, p := range d.Profiles {
			nodes[p.ID] = p
		}
		seen := map[string]bool{}
		profiles := []store.Profile{}
		var collect func(string)
		collect = func(id string) {
			if seen[id] {
				return
			}
			seen[id] = true
			if p, ok := nodes[id]; ok {
				if p.Protocol != "hysteria2" && p.Protocol != "hysteria" {
					profiles = append(profiles, p)
				}
				return
			}
			for _, member := range groups[id].Members {
				collect(member)
			}
		}
		collect(groupID)
		problem := ""
		if _, exists := groups[groupID]; !exists {
			problem = "策略组已不存在"
		}
		if len(profiles) == 0 && problem == "" {
			problem = "本组没有可检测的 TCP 节点"
		}
		if len(profiles) > 256 {
			problem = "组内 TCP 节点超过 256 个，请先缩小分组"
		}
		if problem != "" {
			s.state.Update(func(sn *Snapshot) { sn.GroupResult = problem })
			return
		}
		s.state.Update(func(sn *Snapshot) { sn.GroupResult = "" })
		s.proberForSettings(d.Settings).Start(profiles)
	})
}
