package app

import (
	"errors"
	"log/slog"

	"github.com/sbgayhub/vray/internal/paths"
)

// SetDataRoot 记录下一次启动使用的数据目录。
//
// 操作只写入稳定定位文件；应用重启后，路径层会在打开数据库、日志和
// 凭据之前复制旧目录中的文件。这样不会在运行中的存储、日志写入器
// 仍在工作时搬动文件。
func (s *Service) SetDataRoot(target string) <-chan error {
	return s.changeDataRoot(func() error {
		return s.paths.SetCustomRoot(target)
	})
}

// ResetDataRoot 记录下一次启动迁回平台默认数据目录。
func (s *Service) ResetDataRoot() <-chan error {
	return s.changeDataRoot(func() error {
		return s.paths.SetDefaultRoot()
	})
}

// changeDataRoot 串行执行目录变更，并把结果发布到 UI 状态。
func (s *Service) changeDataRoot(change func() error) <-chan error {
	done := make(chan error, 1)
	if !s.dataRootBusy.CompareAndSwap(false, true) {
		err := errors.New("数据目录正在更改，请稍候")
		done <- err
		close(done)
		return done
	}

	s.state.Update(func(sn *Snapshot) {
		sn.DataRootSaving = true
		sn.DataRootError = ""
	})
	go func() {
		defer s.dataRootBusy.Store(false)

		err := change()
		if err != nil {
			slog.Error("保存数据目录设置失败", "错误", err)
			s.state.Update(func(sn *Snapshot) {
				sn.DataRootSaving = false
				sn.DataRootError = err.Error()
			})
			done <- err
			close(done)
			return
		}

		slog.Info("已保存数据目录设置，重启后将迁移现有文件", "原目录", s.paths.Root)
		s.state.Update(func(sn *Snapshot) {
			sn.DataRootSaving = false
			sn.DataRootPending = true
			sn.DataRootError = ""
		})
		done <- nil
		close(done)
	}()
	return done
}

// dataRootNotice 组合固定原因和路径层警告，供设置页直接显示。
func dataRootNotice(p paths.Paths) string {
	notice := p.ChangeDisabledReason()
	if p.Warning == "" {
		return notice
	}
	if notice == "" {
		return p.Warning
	}
	return notice + "；" + p.Warning
}
