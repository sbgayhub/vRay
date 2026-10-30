package app

// connectForTest 同步执行连接路径，测试无需轮询即可断言结果。
func (s *Service) connectForTest(profileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connectLocked(profileID)
}
