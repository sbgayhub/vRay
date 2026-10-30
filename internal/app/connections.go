package app

import xcore "github.com/sbgayhub/vray/internal/core"

// Connections 读取瞬时连接元数据；它从不加载存储，也不持久化目标/来源地址。
func (s *Service) Connections() []xcore.Connection {
	if source, ok := s.engine.(interface{ Connections() []xcore.Connection }); ok {
		return source.Connections()
	}
	return nil
}
