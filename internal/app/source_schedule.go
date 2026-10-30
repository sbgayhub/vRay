package app

import (
	"context"
	"time"
)

// RunSourceScheduler 只有一个所有者、一个可取消的工作 goroutine 和有界
// tick。刷新串行执行，长列表不会造成网络扇出。
func (s *Service) RunSourceScheduler(ctx context.Context) <-chan struct{} {
	return s.runAsync(func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		attempted := make(map[string]time.Time)
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				s.mu.Lock()
				if s.sampler != nil {
					_ = s.flushTrafficLocked()
				}
				s.mu.Unlock()
				for _, src := range s.state.Snapshot().Sources {
					interval := time.Duration(src.IntervalMinutes) * time.Minute
					if !src.Enabled || src.Updating || interval < 15*time.Minute {
						continue
					}
					if now.Sub(src.LastFetchedAt) < interval || now.Sub(attempted[src.ID]) < interval {
						continue
					}
					attempted[src.ID] = now
					select {
					case <-ctx.Done():
						return
					case <-s.refreshSourceIntent(ctx, src.ID):
					}
				}
			}
		}
	})
}
