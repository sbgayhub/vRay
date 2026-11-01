package ui

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/store"
)

type policyBrowser struct {
	groupQuery, memberQuery, memberKind             string
	lastGroupQuery, lastMemberQuery, lastMemberKind string
	navigationGroup                                 string
	sortLatency                                     bool
	trail                                           []string
	navigation, members                             ui.ListState
	health                                          map[string]xcore.PolicyHealth
	nextHealth                                      time.Time
	healthSince                                     time.Time
	healthGroup                                     string
	pending                                         <-chan policyHealthResult
}

type policyHealthResult struct {
	since  time.Time
	group  string
	health map[string]xcore.PolicyHealth
}

// 对现有核心观察器进行一次合并的按需读取。UI 从不等待引擎操作，离开此页面后也不会再安排工作。
func (r *Root) refreshPolicyHealth(c *ui.Context, snap app.Snapshot) {
	b := &r.groups
	if b.pending != nil {
		select {
		case result := <-b.pending:
			b.pending = nil
			if result.since.Equal(snap.Since) && result.group == snap.Settings.ActiveGroup {
				b.health = result.health
			}
		default:
		}
	}
	if !b.healthSince.Equal(snap.Since) || b.healthGroup != snap.Settings.ActiveGroup {
		b.health, b.nextHealth = nil, time.Time{}
		b.healthSince, b.healthGroup = snap.Since, snap.Settings.ActiveGroup
	}
	if snap.Phase != app.PhaseConnected || snap.Settings.ActiveGroup == "" {
		b.health = nil
		return
	}
	c.After(2 * time.Second)
	if b.pending != nil {
		c.After(100 * time.Millisecond)
	}
	if b.pending != nil || c.Now().Before(b.nextHealth) {
		return
	}
	b.nextHealth = c.Now().Add(2 * time.Second)
	done := make(chan policyHealthResult, 1)
	b.pending = done
	svc, since, group := r.svc, snap.Since, snap.Settings.ActiveGroup
	go func() {
		done <- policyHealthResult{since: since, group: group, health: svc.PolicyHealth()}
	}()
}

type policyIndex struct {
	groups  map[string]store.PolicyGroup
	nodes   map[string]app.ProfileView
	sources map[string]string
	used    map[string]bool
}

func indexPolicies(snap app.Snapshot) policyIndex {
	index := policyIndex{
		groups:  make(map[string]store.PolicyGroup, len(snap.Groups)),
		nodes:   make(map[string]app.ProfileView, len(snap.Profiles)),
		sources: make(map[string]string, len(snap.Sources)),
		used:    map[string]bool{},
	}
	for _, g := range snap.Groups {
		index.groups[g.ID] = g
	}
	for _, p := range snap.Profiles {
		index.nodes[p.ID] = p
	}
	for _, source := range snap.Sources {
		index.sources[source.ID] = source.Name
	}
	queue := []string{snap.Settings.ActiveGroup}
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		g, ok := index.groups[id]
		if !ok || index.used[id] {
			continue
		}
		index.used[id] = true
		if g.Type == "select" {
			queue = append(queue, policySelection(g))
		} else {
			queue = append(queue, g.Members...)
		}
	}
	return index
}

// policiesIndex 返回当前状态的策略索引，并按状态版本号缓存。
//
// 组页与编辑弹窗每帧各调一次 indexPolicies（建 4 个 map 加一次 BFS）；
// 索引只取决于已提交的快照，而版本号在任何状态变更时递增，因此空闲帧
// 可以直接复用上一次的结果。
func (r *Root) policiesIndex(snap app.Snapshot) policyIndex {
	ver := r.svc.State().Version()
	if ver == r.policyIndexVer && r.policyIndexCache.groups != nil {
		return r.policyIndexCache
	}
	index := indexPolicies(snap)
	r.policyIndexVer, r.policyIndexCache = ver, index
	return index
}

func (index policyIndex) name(id string) string {
	if p, ok := index.nodes[id]; ok {
		return p.Name
	}
	if g, ok := index.groups[id]; ok {
		return g.Name
	}
	if id == "DIRECT" {
		return "DIRECT"
	}
	if id == "REJECT" || id == "REJECT-DROP" {
		return "REJECT"
	}
	return "成员已移除"
}

func policySelection(g store.PolicyGroup) string {
	if g.Selected != "" {
		return g.Selected
	}
	if len(g.Members) > 0 {
		return g.Members[0]
	}
	return ""
}

func (index policyIndex) path(id string) []string {
	path := []string{}
	seen := map[string]bool{}
	for range 64 {
		g, ok := index.groups[id]
		if !ok || g.Type != "select" {
			break
		}
		if seen[id] {
			path = append(path, "循环引用")
			break
		}
		seen[id] = true
		id = policySelection(g)
		path = append(path, index.name(id))
	}
	return path
}

func (r *Root) openPolicy(id string, nested bool) {
	if id == r.selectedGroup {
		return
	}
	if nested {
		if at := slices.Index(r.groups.trail, id); at >= 0 {
			r.groups.trail = r.groups.trail[:at]
		} else if len(r.groups.trail) < 32 {
			r.groups.trail = append(r.groups.trail, r.selectedGroup)
		}
	} else {
		r.groups.trail = nil
	}
	r.selectedGroup = id
	r.groups.memberQuery, r.groups.memberKind = "", ""
	r.groups.members.ScrollTo(0, ui.Start)
}

func (index policyIndex) filterGroups(groups []store.PolicyGroup, query string) []store.PolicyGroup {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return groups
	}
	result := []store.PolicyGroup{}
	for _, g := range groups {
		if strings.Contains(strings.ToLower(g.Name+" "+index.sources[g.SourceID]), q) {
			result = append(result, g)
		}
	}
	return result
}

func (b *policyBrowser) filterMembers(index policyIndex, g store.PolicyGroup, latencies map[string]app.Latency) []string {
	q := strings.ToLower(strings.TrimSpace(b.memberQuery))
	result := make([]string, 0, len(g.Members))
	for _, id := range g.Members {
		_, isGroup := index.groups[id]
		_, isNode := index.nodes[id]
		if b.memberKind == "groups" && !isGroup {
			continue
		}
		if b.memberKind == "nodes" && !isNode {
			continue
		}
		p := index.nodes[id]
		search := index.name(id) + " " + p.Protocol + " " + p.Address + " " + index.sources[p.SourceID]
		if q == "" || strings.Contains(strings.ToLower(search), q) {
			result = append(result, id)
		}
	}
	if b.sortLatency {
		slices.SortStableFunc(result, func(a, bid string) int {
			am, aok := b.delay(a, latencies)
			bm, bok := b.delay(bid, latencies)
			if aok && !bok {
				return -1
			}
			if bok && !aok {
				return 1
			}
			if !aok && !bok {
				return 0
			}
			if am < bm {
				return -1
			}
			if am > bm {
				return 1
			}
			return 0
		})
	}
	return result
}

func (b *policyBrowser) delay(id string, latencies map[string]app.Latency) (int64, bool) {
	if health, ok := b.health[id]; ok {
		return health.Delay, health.Alive
	}
	latency := latencies[id]
	return latency.MS, latency.State == app.LatencyOK
}
