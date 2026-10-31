package app

import (
	"runtime/debug"
	"time"

	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/diag"
	"github.com/sbgayhub/vray/internal/store"
)

// Dashboard 是瞬时遥测，不会进入数据库。
type Dashboard struct {
	At               time.Time
	Memory           diag.MemoryStats
	Connections      int
	MemoryReleasing  bool
	MemoryReleasedAt time.Time
	PolicyExits      xcore.PolicyExits
	PolicySince      time.Time
}

// RefreshDashboard 由可见的首页按需驱动。内存采样不占用 UI 线程、
// 会合并请求，且不创建后台定时器。
func (s *Service) RefreshDashboard() {
	s.dashboardMu.Lock()
	s.state.mu.RLock()
	at := s.state.snap.Dashboard.At
	phase, since, group := s.state.snap.Phase, s.state.snap.Since, s.state.snap.Settings.ActiveGroup
	s.state.mu.RUnlock()
	if s.dashboardBusy || s.memoryRelease != nil || (!at.IsZero() && time.Since(at) < 2*time.Second) {
		s.dashboardMu.Unlock()
		return
	}
	s.dashboardBusy = true
	s.dashboardMu.Unlock()
	go func() {
		value := Dashboard{At: time.Now(), Memory: diag.ReadMemory(), Connections: s.ConnectionCount()}
		if phase == PhaseConnected && group != "" {
			if source, ok := s.engine.(interface{ PolicyExits() xcore.PolicyExits }); ok {
				value.PolicyExits = source.PolicyExits()
			}
		}
		s.state.Update(func(sn *Snapshot) {
			if value.At.After(sn.Dashboard.At) {
				sn.Dashboard.At = value.At
				sn.Dashboard.Memory = value.Memory
				sn.Dashboard.Connections = value.Connections
				if sn.Phase == phase && sn.Since.Equal(since) && sn.Settings.ActiveGroup == group {
					sn.Dashboard.PolicyExits = value.PolicyExits
					sn.Dashboard.PolicySince = since
				}
			}
		})
		s.dashboardMu.Lock()
		s.dashboardBusy = false
		s.dashboardMu.Unlock()
	}()
}

// ReleaseMemory 执行 GC 并把未使用的 Go 堆页归还操作系统。它只由显式
// 请求触发，从不周期执行；并发请求共享同一次运行。
func (s *Service) ReleaseMemory() <-chan struct{} {
	s.dashboardMu.Lock()
	if s.memoryRelease != nil {
		done := s.memoryRelease
		s.dashboardMu.Unlock()
		return done
	}
	done := make(chan struct{})
	s.memoryRelease = done
	s.dashboardMu.Unlock()
	s.state.Update(func(sn *Snapshot) { sn.Dashboard.MemoryReleasing = true })
	go func() {
		// FreeOSMemory 已包含 GC；再单独调用 runtime.GC 会收集两次。
		debug.FreeOSMemory()
		now, memory, connections := time.Now(), diag.ReadMemory(), s.ConnectionCount()
		s.state.Update(func(sn *Snapshot) {
			sn.Dashboard.At = now
			sn.Dashboard.Memory = memory
			sn.Dashboard.Connections = connections
			sn.Dashboard.MemoryReleasing = false
			sn.Dashboard.MemoryReleasedAt = now
		})
		s.dashboardMu.Lock()
		s.memoryRelease = nil
		close(done)
		s.dashboardMu.Unlock()
	}()
	return done
}

func (s *Service) ConnectionCount() int {
	if source, ok := s.engine.(interface{ ConnectionCount() int }); ok {
		return source.ConnectionCount()
	}
	return len(s.Connections())
}

func (s *Service) Latency(id string) Latency {
	s.viewMu.RLock()
	p := s.prober
	s.viewMu.RUnlock()
	if p == nil {
		return Latency{}
	}
	return p.Result(id)
}

// TestNode 只测量指定节点；现有的 TCP 端点探测不需要凭据。
func (s *Service) TestNode(id string) {
	if id == "" {
		return
	}
	go func() {
		d, err := s.store.Load()
		if err != nil {
			s.setProblem("STORE_ERROR", "读取配置失败", err)
			return
		}
		for _, profile := range d.Profiles {
			if profile.ID == id {
				s.proberForSettings(d.Settings).Start([]store.Profile{profile})
				return
			}
		}
		s.setProblem("NO_PROFILE", "找不到该节点", nil)
	}()
}
