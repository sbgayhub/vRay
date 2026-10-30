package app

import "time"

// UseProfile 让详情面板的连接操作干净地切换节点。
func (s *Service) UseProfile(id string) {
	go func() {
		<-s.SetActive(id)
		if s.state.Snapshot().Phase == PhaseConnected {
			s.Disconnect()
			waitFor(s.state, PhaseIdle, 5*time.Second)
			if s.state.Snapshot().Phase != PhaseIdle {
				return
			}
		}
		s.Connect(id)
	}()
}
