package app

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sbgayhub/vray/internal/core/effects"
	"github.com/sbgayhub/vray/internal/sysproxy"
)

// proxyLayer 是效果日志中的系统代理部分。
//
// 它是唯一写入本机代理设置的地方，并且总是先记录：在“已记录”与“已变更”
// 之间崩溃会留下一条还原未发生变更的条目（无害）；反过来则会留下无人
// 知晓的代理——这正是整套机制要避免的故障。
type proxyLayer struct {
	backend sysproxy.Backend
}

// NewProxyLayer 在代理后端之上返回一层。应用使用平台实现；测试传入假实现。
func NewProxyLayer(b sysproxy.Backend) EffectLayer { return &proxyLayer{backend: b} }

// Enable 为给定本地端口开启系统代理。
func (p *proxyLayer) Enable(set sysproxy.Settings) (effects.Entry, error) {
	// 先捕获当前状态：它既是日志需要的备份，也是还原时要放回去的内容。
	before, err := p.backend.Current()
	if err != nil {
		return effects.Entry{}, fmt.Errorf("读取当前系统代理失败：%w", err)
	}
	backup, err := json.Marshal(before)
	if err != nil {
		return effects.Entry{}, err
	}
	entry := effects.Entry{
		Kind:   effects.KindSystemProxy,
		Target: fmt.Sprintf("%s:%d", set.Host, set.Port),
		Backup: string(backup),
	}
	if _, err := p.backend.Enable(set); err != nil {
		return entry, fmt.Errorf("启用系统代理失败：%w", err)
	}
	return entry, nil
}

// Restore 实现 EffectLayer。
func (p *proxyLayer) Restore(e effects.Entry) error {
	if e.Kind != effects.KindSystemProxy {
		return fmt.Errorf("代理层收到无法还原的条目类型 %s", e.Kind)
	}
	// 开机自启条目不是代理变更；应用自行处理它们，只是为了共用一份日志
	// 才归到本类型下。
	if e.Target == "login-item" {
		return nil
	}
	if e.Backup == "" {
		// 没有捕获任何内容，也就没有东西要放回。在这里报错会让该条目
		// 永远卡在日志里。
		return nil
	}
	var prev sysproxy.Previous
	if err := json.Unmarshal([]byte(e.Backup), &prev); err != nil {
		return fmt.Errorf("副作用日志中的系统代理备份无法读取：%w", err)
	}
	if err := p.backend.Restore(prev); err != nil {
		return fmt.Errorf("还原系统代理失败：%w", err)
	}
	return nil
}

// ApplySystemProxy 开启系统代理并记录到日志。
//
// 只由显式的用户操作调用（在系统代理模式下连接），绝不从每帧绘制中调用。
func (s *Service) ApplySystemProxy(host string, port int) error {
	s.mu.Lock()
	layer, ok := s.systemProxy.(*proxyLayer)
	s.mu.Unlock()

	if !ok {
		// 没有平台层：无需变更，也无需失败。
		return nil
	}
	return s.applySystemProxyLocked(layer, host, port)
}

// applySystemProxyLocked 是供已持有 mu 的调用方使用的 ApplySystemProxy
// （connect 通过 connectLocked 持有）。
//
// 它本身绝不能获取 mu，否则连接路径会自锁。
func (s *Service) applySystemProxyLocked(layer *proxyLayer, host string, port int) error {
	j := s.effectsLocked()
	entry, err := layer.Enable(sysproxy.Settings{
		Host: host, Port: port, Bypass: sysproxy.BypassDefault,
	})
	if err != nil {
		return err
	}
	// 这次在变更之后记录，因为只有读取当前状态后才有备份——enable 调用
	// 负责读取。两者之间的窗口只是一次注册表写入的时长。
	if err := j.Record(entry); err != nil {
		// 变更已应用但未能记入日志：撤销它，而不是留下一个崩溃后会
		// 被困住的代理。
		_ = layer.Restore(entry)
		return fmt.Errorf("记录系统代理变更失败：%w", err)
	}
	return nil
}

// SystemProxyEnabled 报告系统代理当前是否指向 vRay 的本地端口。
//
// 它读取实际状态而不是依赖记忆，因此在 Windows 设置中手动关闭代理的用户
// 看到的是真相，而不是过期的勾选状态。
func (s *Service) SystemProxyEnabled() bool {
	s.mu.Lock()
	layer, ok := s.systemProxy.(*proxyLayer)
	s.mu.Unlock()
	if !ok {
		return false
	}
	cur, err := layer.backend.Current()
	if err != nil {
		return false
	}
	return cur.Enabled
}

// ErrNoSystemProxy 表示当前平台不允许 vRay 设置系统代理。
var ErrNoSystemProxy = errors.New("当前平台不支持设置系统代理")
