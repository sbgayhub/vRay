package app

import (
	"log/slog"

	"github.com/sbgayhub/vray/internal/core/effects"
)

// Effects 是 vRay 记录系统变更的地方。
//
// 三层崩溃补偿都经由这里：
//
//  1. 断开连接时在停止内核前撤销已记录的效果；
//  2. 外壳在退出钩子里调用 RestoreEffects；
//  3. 启动时执行 RecoverEffects，这是能扛住进程被杀的最后防线。
//
// 日志按需打开，从不改系统设置的服务就不会碰磁盘。

// Effects 返回效果日志，首次使用时打开。
//
// 无法读取的日志由日志自身隔离，并在这里报告一次；服务保留一个可用的
// （空的）日志，应用照常运行。
func (s *Service) Effects() *effects.Journal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.effectsLocked()
}

// effectsLocked 在日志尚未打开时打开它。调用方持有 mu。
func (s *Service) effectsLocked() *effects.Journal {
	if s.effectsJournal != nil {
		return s.effectsJournal
	}
	j, entries, err := effects.Open(s.paths.Effects())
	if err != nil {
		// 只报告，不终止：日志读不出来只值得警告，不值得拒绝启动代理。
		slog.Warn("读取系统变更日志失败", "错误", err)
	}
	s.effectsJournal = j
	if len(entries) > 0 {
		slog.Warn("系统变更日志中存在未还原的条目", "数量", len(entries))
	}
	return j
}

// restorer 是本构建撤销一条日志记录的方式。
//
// 绝不通过执行命令字符串来还原：携带 shell 命令的日志一旦被缺陷或恶意
// 文件写入就会被执行。每种变更都有各自的类型化撤销。
func (s *Service) restorer() effects.Restorer {
	return func(e effects.Entry) error {
		switch e.Kind {
		case effects.KindSystemProxy:
			return s.systemProxy.Restore(e)
		case effects.KindDNS:
			return s.dns.Restore(e)
		case effects.KindRoute, effects.KindTun:
			// 路由和设备随 tun 接口消失而移除，Stop() 正是这么做的；
			// 这里没有剩余工作。
			return nil
		default:
			// 日志在打开时会拒绝未知类型，因此实际到不了这里；返回 nil
			// 是为了让一条意外记录不阻塞其余恢复。
			return nil
		}
	}
}

// RecoverEffects 是启动自检：若上次运行留下了系统变更，现在撤销。
//
// 它返回已还原的记录，让外壳能告诉用户发生过什么。悄悄把别人的代理
// 设置改回去比崩溃本身更糟：用户将无从知道网络为何变化。
func (s *Service) RecoverEffects() ([]effects.Entry, error) {
	s.mu.Lock()
	j := s.effectsLocked()
	s.mu.Unlock()

	entries := j.Entries()
	if len(entries) == 0 {
		return nil, nil
	}
	recovered, err := effects.RecoverFromCrash(s.paths.Effects(), s.restorer())
	if len(recovered) > 0 {
		s.state.Update(func(sn *Snapshot) {
			sn.Recovered = len(recovered)
		})
	}
	return recovered, err
}

// RestoreEffects 撤销全部已记录的变更，可重复执行。
func (s *Service) RestoreEffects() error {
	s.mu.Lock()
	j := s.effectsLocked()
	s.mu.Unlock()
	return j.RestoreAll(s.restorer())
}

// SaveLoginItem 记录用户的开机自启选择并立即应用。
//
// 与其他变更一样记入日志：卸载后遗留的自启项虽是小事，但属同类问题，
// 用同一套机制处理，只需在一处做对。
func (s *Service) SaveLoginItem(on bool) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		j := s.effectsLocked()
		if on {
			// 先记录后变更：两步之间崩溃只会留下一条撤销“未发生之事”
			// 的日志，无害；反过来则会留下无人知晓的自启项。
			_ = j.Record(effects.Entry{
				Kind:    effects.KindSystemProxy,
				Target:  "login-item",
				Restore: "remove",
			})
		}
		if s.loginSetter != nil {
			if err := s.loginSetter(on); err != nil {
				s.setProblem("LOGIN_ITEM_FAILED", "设置开机自启失败", err)
				return
			}
		}
		d, err := s.store.Load()
		if err != nil {
			s.setProblem("STORE_ERROR", "读取配置失败", err)
			return
		}
		d.Settings.OpenAtLogin = on
		if err := s.store.SetSettings(d.Settings); err != nil {
			s.setProblem("STORE_ERROR", "保存设置失败", err)
			return
		}
		if !on {
			// 已关闭：对应的日志记录不再需要。
			_ = j.Clear()
		}
		if err := s.Load(); err != nil {
			s.setProblem("STORE_ERROR", "读取配置失败", err)
		}
	}()
}
