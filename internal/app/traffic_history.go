package app

import (
	"time"

	"github.com/sbgayhub/vray/internal/store"
)

// TrafficPoint 是真实采样器的观测值，绝不是 UI 生成的值。
type TrafficPoint struct {
	At      time.Time `json:"at"`
	UpBps   int64     `json:"upBps"`
	DownBps int64     `json:"downBps"`
}

func (s *Service) recordTraffic(id string, previous *store.Transfer, traffic Traffic) {
	up := max(0, traffic.TotalUp-previous.Upload)
	down := max(0, traffic.TotalDown-previous.Download)
	previous.Upload, previous.Download = traffic.TotalUp, traffic.TotalDown
	s.state.Update(func(sn *Snapshot) {
		sn.Traffic = traffic
		if sn.Usage == nil {
			sn.Usage = make(map[string]store.Transfer)
		}
		if id != "" {
			usage := sn.Usage[id]
			usage.Upload += up
			usage.Download += down
			sn.Usage[id] = usage
		}
		now := time.Now()
		sn.History = append(sn.History, TrafficPoint{At: now, UpBps: traffic.UpBps, DownBps: traffic.DownBps})
		first := 0
		for first < len(sn.History) && now.Sub(sn.History[first].At) > TrafficHistoryWindow {
			first++
		}
		if len(sn.History)-first > TrafficHistoryPoints {
			first = len(sn.History) - TrafficHistoryPoints
		}
		if first > 0 {
			sn.History = append([]TrafficPoint(nil), sn.History[first:]...)
		}
	})
}

func (s *Service) flushTrafficLocked() error {
	d, err := s.store.Load()
	if err != nil {
		return err
	}
	d.Usage = s.state.Snapshot().Usage
	return s.store.Save(d)
}

// FlushTraffic 在正常退出、内核拆除前调用。
func (s *Service) FlushTraffic() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sampler != nil {
		s.sampler.Stop()
		s.sampler = nil
	}
	s.closeStatsLocked()
	return s.flushTrafficLocked()
}
