package app

import (
	"fmt"
	"regexp"
)

var accentPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// SaveAppearance 只修改外观字段，保留设置草稿以及同时发生的连接或订阅变更。
func (s *Service) SaveAppearance(theme, material, accent string) <-chan struct{} {
	return s.runAsync(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		validTheme := theme == "system" || theme == "light" || theme == "dark"
		validMaterial := material == "none" || material == "mica" || material == "micaAlt" ||
			material == "acrylic" || material == "liquidGlass"
		validAccent := accent == "system" || accentPattern.MatchString(accent)
		if !validTheme || !validMaterial || !validAccent {
			s.setProblem("APPEARANCE_INVALID", "外观设置无效", nil)
			return
		}
		d, err := s.store.Load()
		if err == nil {
			d.Settings.Theme, d.Settings.Material, d.Settings.Accent = theme, material, accent
			err = s.store.SetSettings(d.Settings)
		}
		if err != nil {
			s.setProblem("STORE_ERROR", "无法保存外观", fmt.Errorf("保存外观：%w", err))
			return
		}
		s.state.Update(func(sn *Snapshot) {
			sn.Settings.Theme, sn.Settings.Material, sn.Settings.Accent = theme, material, accent
		})
	})
}
