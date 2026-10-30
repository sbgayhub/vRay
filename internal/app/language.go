package app

func (s *Service) SaveLanguage(language string) <-chan struct{} {
	return s.runAsync(func() {
		if language != "zh-Hans" && language != "zh-Hant" && language != "en" {
			s.setProblem("LANGUAGE_INVALID", "不支持的语言", nil)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		d, err := s.store.Load()
		if err == nil {
			d.Settings.Language = language
			err = s.store.SetSettings(d.Settings)
		}
		if err != nil {
			s.setProblem("STORE_ERROR", "保存语言失败", err)
			return
		}
		s.state.Update(func(sn *Snapshot) { sn.Settings.Language = language })
	})
}
