package app

import (
	"context"
	"strings"

	"github.com/sbgayhub/vray/internal/store"
)

// 这个包装器归单个采样器所有。分组计数始终归属于实际承载流量的节点，
// 而不是先前选中的节点。
type policyCounterSource struct {
	StatsSource
	service *Service
	nodes   map[string]string
	last    map[string]int64
	ready   bool
}

func (s *policyCounterSource) Counters(ctx context.Context, names []string, reset bool) (map[string]int64, error) {
	counters, err := s.StatsSource.Counters(ctx, names, reset)
	if err != nil {
		return nil, err
	}
	deltas := map[string]store.Transfer{}
	for name, value := range counters {
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 {
			continue
		}
		id := s.nodes[parts[1]]
		if id == "" {
			continue
		}
		if s.ready {
			delta := max(0, value-s.last[name])
			usage := deltas[id]
			if parts[3] == "uplink" {
				usage.Upload += delta
			} else if parts[3] == "downlink" {
				usage.Download += delta
			}
			deltas[id] = usage
		}
		s.last[name] = value
	}
	s.ready = true
	if len(deltas) > 0 {
		s.service.state.Update(func(sn *Snapshot) {
			if sn.Usage == nil {
				sn.Usage = map[string]store.Transfer{}
			}
			for id, delta := range deltas {
				usage := sn.Usage[id]
				usage.Upload += delta.Upload
				usage.Download += delta.Download
				sn.Usage[id] = usage
			}
		})
	}
	return counters, nil
}
