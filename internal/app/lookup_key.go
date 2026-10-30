package app

import (
	"strings"
	"time"
)

// SaveIPLookupKey 把可选的 IP 查询服务商密钥保存到凭据保管器。
func (s *Service) SaveIPLookupKey(key string) <-chan struct{} {
	return s.runAsync(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		key = strings.TrimSpace(key)
		var err error
		if key == "" {
			err = s.secrets.Delete("preferences/ipapi-key")
		} else {
			err = s.secrets.Set("preferences/ipapi-key", []byte(key))
		}
		if err != nil {
			s.setProblem("IP_KEY_ERROR", "无法保存 IP 查询密钥", nil)
			return
		}
		d, err := s.store.Load()
		if err == nil {
			d.Settings.LookupKeyConfigured = key != ""
			err = s.store.SetSettings(d.Settings)
		}
		if err != nil {
			s.setProblem("IP_KEY_ERROR", "无法保存 IP 查询设置", nil)
			return
		}
		s.state.Update(func(sn *Snapshot) { sn.Settings.LookupKeyConfigured = key != "" })
		s.viewMu.Lock()
		s.ipRetryAt = time.Time{}
		s.viewMu.Unlock()
	})
}
