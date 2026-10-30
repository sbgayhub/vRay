package app

import (
	"context"
	"errors"
	"time"
)

// ErrUpdateUnavailable 表示当前构建或安装位置无法安全地自我替换。
var ErrUpdateUnavailable = errors.New("应用更新不可用")

// UpdateRelease 是一个已签名版本在应用层的视图。handle 属于后端，
// 永远不会进入 Snapshot。
type UpdateRelease struct {
	Version string
	Notes   string
	Date    time.Time
	handle  any
}

// UpdateBackend 由 MyGO 的签名更新器实现。保持接口精简，测试无需联网
// 即可验证调度、错误与进度。
type UpdateBackend interface {
	Enabled() bool
	Check(context.Context) (*UpdateRelease, error)
	Install(context.Context, *UpdateRelease, func(downloaded, total int64)) error
	Relaunch()
}

// UpdateView 可以安全地复制进 UI 快照。
type UpdateView struct {
	AvailableVersion string    `json:"availableVersion,omitempty"`
	Notes            string    `json:"notes,omitempty"`
	Error            string    `json:"error,omitempty"`
	ReleaseDate      time.Time `json:"releaseDate,omitempty"`
	LastCheckedAt    time.Time `json:"lastCheckedAt,omitempty"`
	Downloaded       int64     `json:"downloaded,omitempty"`
	Total            int64     `json:"total,omitempty"`
	Enabled          bool      `json:"enabled"`
	Checking         bool      `json:"checking"`
	Installing       bool      `json:"installing"`
	Installed        bool      `json:"installed"`
	UpToDate         bool      `json:"upToDate"`
}

// AttachUpdateBackend 在应用启动后接入签名更新器。
func (s *Service) AttachUpdateBackend(backend UpdateBackend) {
	enabled := backend != nil && backend.Enabled()
	s.updateMu.Lock()
	s.updateBackend = backend
	s.updateMu.Unlock()
	s.state.Update(func(sn *Snapshot) {
		sn.Update.Enabled = enabled
		if !enabled {
			sn.Update.Error = "当前构建或安装位置不支持自动更新"
		}
	})
}

// CheckForUpdates 发起一次手动或定时更新检查。并发请求会合并，
// 因此本进程对更新源最多只有一个请求。
func (s *Service) CheckForUpdates() {
	if s.state.Snapshot().ConsentRequired {
		return
	}
	s.updateMu.Lock()
	if s.updateBusy {
		s.updateMu.Unlock()
		return
	}
	backend := s.updateBackend
	base := s.updateRootContext
	s.updateMu.Unlock()
	if backend == nil || !backend.Enabled() {
		s.state.Update(func(sn *Snapshot) {
			sn.Update.Enabled = false
			sn.Update.Error = "当前构建或安装位置不支持自动更新"
		})
		return
	}
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, 30*time.Second)

	s.updateMu.Lock()
	if s.updateBusy {
		s.updateMu.Unlock()
		cancel()
		return
	}
	s.updateBusy = true
	s.updateMu.Unlock()
	s.state.Update(func(sn *Snapshot) {
		sn.Update.Checking = true
		sn.Update.Error = ""
		sn.Update.UpToDate = false
		sn.Update.Installed = false
	})

	go func() {
		defer cancel()
		release, err := backend.Check(ctx)
		now := time.Now()
		s.updateMu.Lock()
		s.updateBusy = false
		if err == nil {
			s.updateRelease = release
		}
		s.updateMu.Unlock()

		s.state.Update(func(sn *Snapshot) {
			sn.Update.Checking = false
			sn.Update.LastCheckedAt = now
			switch {
			case err != nil:
				sn.Update.Error = updateErrorText(err)
				if errors.Is(err, ErrUpdateUnavailable) {
					sn.Update.Enabled = false
				}
			case release == nil:
				sn.Update.AvailableVersion = ""
				sn.Update.Notes = ""
				sn.Update.ReleaseDate = time.Time{}
				sn.Update.UpToDate = true
				sn.Update.Installed = false
			default:
				sn.Update.AvailableVersion = release.Version
				sn.Update.Notes = release.Notes
				sn.Update.ReleaseDate = release.Date
				sn.Update.UpToDate = false
				sn.Update.Installed = false
			}
		})
	}()
}

// InstallUpdate 下载、校验并暂存可用的签名版本。
// 在 RelaunchForUpdate 之前，运行中的进程保持原样。
func (s *Service) InstallUpdate() {
	s.updateMu.Lock()
	if s.updateBusy || s.updateBackend == nil || s.updateRelease == nil {
		s.updateMu.Unlock()
		return
	}
	backend, release, base := s.updateBackend, s.updateRelease, s.updateRootContext
	s.updateBusy = true
	s.updateMu.Unlock()
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, 45*time.Minute)

	s.state.Update(func(sn *Snapshot) {
		sn.Update.Installing = true
		sn.Update.Installed = false
		sn.Update.Error = ""
		sn.Update.Downloaded = 0
		sn.Update.Total = 0
	})
	go func() {
		defer cancel()
		var lastPaint time.Time
		progress := func(downloaded, total int64) {
			if now := time.Now(); !lastPaint.IsZero() && now.Sub(lastPaint) < 100*time.Millisecond {
				return
			} else {
				lastPaint = now
			}
			if downloaded < 0 {
				downloaded = 0
			}
			if total >= 0 && downloaded > total && total > 0 {
				downloaded = total
			}
			s.state.Update(func(sn *Snapshot) {
				sn.Update.Downloaded = downloaded
				sn.Update.Total = total
			})
		}
		err := backend.Install(ctx, release, progress)

		s.updateMu.Lock()
		s.updateBusy = false
		s.updateMu.Unlock()
		s.state.Update(func(sn *Snapshot) {
			sn.Update.Installing = false
			if err != nil {
				sn.Update.Error = updateErrorText(err)
				return
			}
			sn.Update.Installed = true
		})
	}()
}

// RelaunchForUpdate 启动已暂存的版本。MyGO 的重启也会执行正常退出钩子，
// 先还原系统代理与流量状态。
func (s *Service) RelaunchForUpdate() {
	s.updateMu.Lock()
	backend, installed := s.updateBackend, s.state.Snapshot().Update.Installed
	s.updateMu.Unlock()
	if backend != nil && installed {
		backend.Relaunch()
	}
}

// StartUpdateScheduler 启动每天 04:00 的自动检查。只能在用户接受
// 法律条款之后调用。
func (s *Service) StartUpdateScheduler(ctx context.Context) {
	s.updateScheduleOnce.Do(func() {
		s.updateMu.Lock()
		s.updateRootContext = ctx
		s.updateMu.Unlock()
		go s.runUpdateScheduler(ctx)
	})
}

func (s *Service) runUpdateScheduler(ctx context.Context) {
	s.runScheduledUpdate(time.Now())
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.runScheduledUpdate(now)
		}
	}
}

func (s *Service) runScheduledUpdate(now time.Time) {
	snap := s.state.Snapshot()
	if snap.ConsentRequired || !snap.Settings.AutoUpdateChecks || !snap.Update.Enabled || now.Hour() != 4 {
		return
	}
	date := now.Format("2006-01-02")
	s.updateMu.Lock()
	if s.lastAutoUpdateDate == date {
		s.updateMu.Unlock()
		return
	}
	s.lastAutoUpdateDate = date
	s.updateMu.Unlock()
	s.CheckForUpdates()
}

func updateErrorText(err error) string {
	switch {
	case errors.Is(err, ErrUpdateUnavailable):
		return "当前构建或安装位置不支持自动更新"
	case errors.Is(err, context.Canceled):
		return "更新操作已取消"
	case errors.Is(err, context.DeadlineExceeded):
		return "更新操作超时，请稍后重试"
	default:
		return "更新失败：" + err.Error()
	}
}
