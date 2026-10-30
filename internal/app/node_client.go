package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/diag"
)

// clientForNode 总是把请求固定到选中节点，包括其私有凭据与完整传输配置。
func (s *Service) clientForNode(ctx context.Context, selected ProfileView) (*http.Client, func(), error) {
	profile, err := s.store.GetProfile(selected.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("无法读取节点配置")
	}
	if !profile.UpdatedAt.Equal(selected.revision) {
		return nil, nil, fmt.Errorf("节点配置已变化，请重试")
	}
	secret, err := s.secretOf(profile)
	if err != nil {
		return nil, nil, fmt.Errorf("无法读取节点凭据，请重新配置节点")
	}
	factory := s.nodeClient
	if factory == nil {
		provider, ok := s.engine.(interface {
			NodeHTTPClient(context.Context, configgen.Outbound) (*http.Client, func(), error)
		})
		if !ok {
			return nil, nil, fmt.Errorf("当前内核不支持节点网络测试")
		}
		factory = provider.NodeHTTPClient
	}
	client, cleanup, err := factory(ctx, outboundOf(profile, secret))
	if err != nil {
		return nil, nil, fmt.Errorf("无法建立节点测试通道：%s", TrimDetail(diag.Scrub(err.Error())))
	}
	return client, cleanup, nil
}
