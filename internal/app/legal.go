package app

import (
	"time"

	"github.com/sbgayhub/vray/internal/store"
)

// SetPostConsentStart 注册同意后可能访问网络的一次性工作。外壳在显示首帧
// 之前安装它，因此接受条款后能启动与正常启动相同的工作。
func (s *Service) SetPostConsentStart(fn func()) {
	s.mu.Lock()
	s.postConsentStart = fn
	s.mu.Unlock()
}

// StartPostConsent 恰好执行一次同意后的启动工作。用户接受首次运行对话框
// 时，调度器会再次调用它。
func (s *Service) StartPostConsent() {
	if s.state.Snapshot().ConsentRequired {
		return
	}
	s.postConsentOnce.Do(func() {
		s.mu.Lock()
		fn := s.postConsentStart
		s.mu.Unlock()
		if fn != nil {
			fn()
		}
	})
}

// AcceptTerms 保存当前法律条款版本。在原子写入设置成功之前，
// ConsentRequired 保持为 true。
func (s *Service) AcceptTerms() <-chan struct{} {
	return s.runAsync(func() {
		s.mu.Lock()
		d, err := s.store.Load()
		if err != nil {
			s.mu.Unlock()
			s.setProblem("STORE_ERROR", "读取法律协议状态失败", err)
			return
		}
		d.Settings.TermsAcceptedVersion = store.LegalVersion
		d.Settings.TermsAcceptedAt = time.Now()
		if err := s.store.SetSettings(d.Settings); err != nil {
			s.mu.Unlock()
			s.setProblem("STORE_ERROR", "保存法律协议确认失败", err)
			return
		}
		s.mu.Unlock()
		s.state.Update(func(sn *Snapshot) { sn.ConsentRequired = false })
		s.StartPostConsent()
	})
}
