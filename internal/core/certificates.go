package core

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	quic "github.com/apernet/quic-go"
	"github.com/sbgayhub/vray/internal/core/configgen"
)

// PrepareNode 将明确标记为不安全的订阅端点迁移为 Xray 接受的证书固定信息。
// 仅该端点的首次 TLS 握手跳过验证；所有经代理的 HTTPS 请求仍执行正常验证。
// 普通已验证节点绝不会走此路径。不会持久化任何凭据。
func (e *Engine) PrepareNode(ctx context.Context, node configgen.Outbound) (configgen.Outbound, error) {
	if !node.AllowInsecure || node.Security == "reality" || node.PinnedPeerCertSHA256 != "" {
		return node, nil
	}
	serverName := node.SNI
	if serverName == "" {
		serverName = node.Host
	}
	if serverName == "" {
		serverName = node.Address
	}
	address := net.JoinHostPort(node.Address, strconv.Itoa(node.Port))
	key := address + "|" + serverName + "|" + node.Protocol
	e.certificateMu.Lock()
	pin := e.certificatePins[key]
	e.certificateMu.Unlock()
	if pin == "" {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		config := &tls.Config{ServerName: serverName, InsecureSkipVerify: true} // 端点明确要求跳过验证。
		var state tls.ConnectionState
		if node.Protocol == "hysteria2" || node.Protocol == "hysteria" {
			config.NextProtos = []string{"h3"}
			connection, err := quic.DialAddr(ctx, address, config, &quic.Config{HandshakeIdleTimeout: 6 * time.Second})
			if err != nil {
				return node, fmt.Errorf("无法获取节点 QUIC 证书指纹：%w", err)
			}
			state = connection.ConnectionState().TLS
			_ = connection.CloseWithError(0, "")
		} else {
			if node.ALPN != "" {
				config.NextProtos = strings.Split(node.ALPN, ",")
			}
			dialer := tls.Dialer{Config: config}
			connection, err := dialer.DialContext(ctx, "tcp", address)
			if err != nil {
				return node, fmt.Errorf("无法获取节点 TLS 证书指纹：%w", err)
			}
			state = connection.(*tls.Conn).ConnectionState()
			_ = connection.Close()
		}
		if len(state.PeerCertificates) == 0 {
			return node, fmt.Errorf("节点未提供服务器证书")
		}
		hash := sha256.Sum256(state.PeerCertificates[0].Raw)
		pin = hex.EncodeToString(hash[:])
		e.certificateMu.Lock()
		if e.certificatePins == nil {
			e.certificatePins = map[string]string{}
		}
		if len(e.certificatePins) >= 256 {
			clear(e.certificatePins)
		}
		if existing := e.certificatePins[key]; existing != "" {
			pin = existing
		} else {
			e.certificatePins[key] = pin
		}
		e.certificateMu.Unlock()
	}
	node.PinnedPeerCertSHA256 = pin
	return node, nil
}
