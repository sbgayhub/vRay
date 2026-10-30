package app

// SetRoutingMode 独立于流量进入方式持久化出站选择。运行中的内核会在同一把
// 操作锁下重启，使选择立即生效；空闲应用保持空闲。
func (s *Service) SetRoutingMode(mode string) <-chan struct{} {
	done := make(chan struct{})
	started := false
	s.state.Update(func(sn *Snapshot) {
		if !sn.RoutingSaving {
			sn.RoutingSaving = true
			started = true
		}
	})
	if !started {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		defer s.state.Update(func(sn *Snapshot) { sn.RoutingSaving = false })
		s.mu.Lock()
		defer s.mu.Unlock()
		switch mode {
		case "rule", "global", "direct":
		default:
			s.setProblem("SETTINGS_INVALID", "不支持的出站方式", nil)
			return
		}
		d, err := s.store.Load()
		if err != nil {
			s.setProblem("STORE_ERROR", "读取配置失败", err)
			return
		}
		if d.Settings.RoutingMode == mode {
			return
		}
		d.Settings.RoutingMode = mode
		if err := s.store.SetSettings(d.Settings); err != nil {
			s.setProblem("STORE_ERROR", "保存出站方式失败", err)
			return
		}
		snap := s.state.Snapshot()
		s.state.Update(func(sn *Snapshot) { sn.Settings.RoutingMode = mode })
		if snap.Phase != PhaseConnected {
			return
		}
		if err := s.disconnectLocked(); err != nil {
			return
		}
		s.state.SetPhase(PhaseConnecting, nil)
		_ = s.connectLocked(snap.Active)
	}()
	return done
}
