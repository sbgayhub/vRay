package core

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/routing"
)

type PolicyExitNode struct {
	ID, Name, Protocol string
	Connections        int
}

type PolicyExits struct {
	GroupID string
	Source  string // active、selected、recent、waiting、direct、block
	Nodes   []PolicyExitNode
}

// PolicyExits 检查实际会话和 balancer 的只读目标 API。
// 它绝不会调用 PickRoute/PickOutbound，因为那会推进轮询状态。
func (e *Engine) PolicyExits() PolicyExits {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := PolicyExits{Nodes: []PolicyExitNode{}}
	if e.inst == nil || e.policyRoute.GroupID == "" {
		return result
	}
	route := e.policyRoute
	result.GroupID, result.Source = route.GroupID, "waiting"
	tracker := e.connections
	tracker.mu.Lock()
	labels := make(map[string]configgen.PolicyLabel, len(tracker.policyNodes))
	for tag, node := range tracker.policyNodes {
		labels[tag] = node
	}
	counts := map[string]int{}
	bytes := map[string]int64{}
	for _, record := range tracker.active {
		tag := record.info.Outbound
		if _, ok := labels[tag]; ok {
			counts[tag]++
			bytes[tag] += record.up.Load() + record.down.Value()
		}
	}
	last := tracker.lastPolicyTag
	tracker.mu.Unlock()
	tags := []string{}
	for tag := range counts {
		tags = append(tags, tag)
	}
	if len(tags) > 0 {
		result.Source = "active"
		sort.Slice(tags, func(i, j int) bool {
			if bytes[tags[i]] != bytes[tags[j]] {
				return bytes[tags[i]] > bytes[tags[j]]
			}
			return tags[i] < tags[j]
		})
	} else if !route.Balanced {
		tags = []string{route.Tag}
		result.Source = "selected"
	} else if route.Kind == "load-balance" {
		if _, ok := labels[last]; ok {
			tags = []string{last}
			result.Source = "recent"
		}
	} else {
		router := e.inst.GetFeature(routing.RouterType())
		if overrides, ok := router.(routing.BalancerOverrider); ok {
			if tag, err := overrides.GetOverrideTarget(route.Tag); err == nil && tag != "" {
				tags = []string{tag}
			}
		}
		if len(tags) == 0 {
			if targets, ok := router.(routing.BalancerPrincipleTarget); ok {
				if chosen, err := targets.GetPrincipleTarget(route.Tag); err == nil && len(chosen) == 1 && chosen[0] != "" {
					tags = chosen
				}
			}
		}
		if len(tags) == 0 && route.Fallback != "" {
			tags = []string{route.Fallback}
		}
		if len(tags) > 0 {
			result.Source = "selected"
		}
	}
	for _, tag := range tags {
		if tag == configgen.TagDirect {
			result.Source = "direct"
			continue
		}
		if tag == configgen.TagBlock {
			result.Source = "block"
			continue
		}
		if node, ok := labels[tag]; ok {
			result.Nodes = append(result.Nodes, PolicyExitNode{
				ID: node.ID, Name: node.Name, Protocol: node.Protocol, Connections: counts[tag],
			})
		}
	}
	return result
}

// PolicyHealth 是现有核心 HTTP 观测结果，而不是 TCP 端点探测。
type PolicyHealth struct {
	Alive     bool
	Delay     int64
	CheckedAt time.Time
}

// PolicyHealth 读取运行中的观测器，不会发起网络请求。
// 调用方应通过 worker 调用：启动或关闭期间可能暂时持有核心锁。
func (e *Engine) PolicyHealth() map[string]PolicyHealth {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := map[string]PolicyHealth{}
	if e.inst == nil {
		return result
	}
	observer, ok := e.inst.GetFeature(extension.ObservatoryType()).(extension.Observatory)
	if !ok {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	value, err := observer.GetObservation(ctx)
	if err != nil {
		return result
	}
	report, ok := value.(*observatory.ObservationResult)
	if !ok {
		return result
	}
	for _, status := range report.Status {
		if !strings.HasPrefix(status.OutboundTag, configgen.PolicyNodePrefix) {
			continue
		}
		id := strings.TrimPrefix(status.OutboundTag, configgen.PolicyNodePrefix)
		result[id] = PolicyHealth{
			Alive: status.Alive, Delay: status.Delay,
			CheckedAt: time.Unix(max(status.LastTryTime, status.LastSeenTime), 0),
		}
	}
	return result
}

// startPolicies 由运行中的核心持有，并在该核心关闭前停止。
// 回退逻辑保留声明的优先级，并使用 Xray 的真实 HTTP 探测。
func (e *Engine) startPolicies(inst *core.Instance, raw []byte) {
	var config struct {
		Policies configgen.PolicyMetadata `json:"vrayPolicies"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return
	}
	e.policyRoute = config.Policies.Default
	e.connections.mu.Lock()
	e.connections.policyNodes = map[string]configgen.PolicyLabel{}
	for _, n := range config.Policies.Nodes {
		e.connections.policyNodes[n.Tag] = n
	}
	e.connections.mu.Unlock()
	if len(config.Policies.Fallbacks) == 0 {
		return
	}
	observer, ok := inst.GetFeature(extension.ObservatoryType()).(extension.Observatory)
	if !ok {
		return
	}
	overrider, ok := inst.GetFeature(routing.RouterType()).(routing.BalancerOverrider)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.policyCancel = cancel
	e.policyDone = make(chan struct{})
	done := e.policyDone
	go func() {
		defer close(done)
		update := func() {
			observation, err := observer.GetObservation(ctx)
			if err != nil {
				return
			}
			report, ok := observation.(*observatory.ObservationResult)
			if !ok {
				return
			}
			alive := map[string]bool{}
			for _, status := range report.Status {
				alive[status.OutboundTag] = status.Alive
			}
			for _, g := range config.Policies.Fallbacks {
				selected := ""
				for _, tag := range g.Members {
					if healthy, known := alive[tag]; healthy || !known {
						selected = tag
						break
					}
				}
				_ = overrider.SetOverrideTarget(g.Tag, selected)
			}
		}
		update()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				update()
			}
		}
	}()
}
