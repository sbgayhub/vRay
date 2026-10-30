// Package distro 精确注册客户端所需的 Xray 组件。
//
// 它替代 main/distro/all。后者还会引入服务端代理、TOML 和 YAML 加载器
// 以及完整的 CLI 命令树；仅导入该包就测得 43.6 MB（docs/02 §4.3.1）。
// 这里的每个包都会在 init 函数中自行注册，因此组件未注册时，
// LoadConfig 会以 "unknown protocol" 失败，而不是在运行时出现异常行为。
//
// 添加协议：在此处添加其出站包，并在 internal/core/configgen 中添加
// golden 配置，以防遗漏注册再次出现。
package distro

import (
	// 必需：dispatcher 和 proxyman 的两个部分。
	_ "github.com/xtls/xray-core/app/dispatcher"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	_ "github.com/xtls/xray-core/main/json" // vRay 仅输入 JSON

	// 生成的配置使用的功能。
	_ "github.com/xtls/xray-core/app/dns"
	_ "github.com/xtls/xray-core/app/dns/fakedns"
	_ "github.com/xtls/xray-core/app/log"
	_ "github.com/xtls/xray-core/app/observatory"
	_ "github.com/xtls/xray-core/app/policy"
	_ "github.com/xtls/xray-core/app/router"
	_ "github.com/xtls/xray-core/app/stats"

	// API：stats、路由、热切换出站、日志级别。
	_ "github.com/xtls/xray-core/app/commander"
	_ "github.com/xtls/xray-core/app/log/command"
	_ "github.com/xtls/xray-core/app/observatory/command"
	_ "github.com/xtls/xray-core/app/proxyman/command"
	_ "github.com/xtls/xray-core/app/router/command"
	_ "github.com/xtls/xray-core/app/stats/command"

	// 上游要求引入此包以打破导入循环。
	_ "github.com/xtls/xray-core/transport/internet/tagged/taggedimpl"

	// 出站。客户端从不注册服务端入站代理。
	_ "github.com/xtls/xray-core/proxy/blackhole"
	_ "github.com/xtls/xray-core/proxy/dns"
	_ "github.com/xtls/xray-core/proxy/freedom"
	_ "github.com/xtls/xray-core/proxy/http"
	_ "github.com/xtls/xray-core/proxy/shadowsocks"
	_ "github.com/xtls/xray-core/proxy/socks"
	_ "github.com/xtls/xray-core/proxy/trojan"
	_ "github.com/xtls/xray-core/proxy/vless/outbound"
	_ "github.com/xtls/xray-core/proxy/vmess/outbound"
	_ "github.com/xtls/xray-core/proxy/wireguard"

	// 入站：mixed（socks 包）、API 的 dokodemo 和 tun。
	_ "github.com/xtls/xray-core/proxy/dokodemo"
	_ "github.com/xtls/xray-core/proxy/tun"

	// 传输层。
	_ "github.com/xtls/xray-core/transport/internet/grpc"
	_ "github.com/xtls/xray-core/transport/internet/headers/http"
	_ "github.com/xtls/xray-core/transport/internet/headers/noop"
	_ "github.com/xtls/xray-core/transport/internet/httpupgrade"
	_ "github.com/xtls/xray-core/transport/internet/kcp"
	_ "github.com/xtls/xray-core/transport/internet/reality"
	_ "github.com/xtls/xray-core/transport/internet/splithttp" // XHTTP
	_ "github.com/xtls/xray-core/transport/internet/tcp"
	_ "github.com/xtls/xray-core/transport/internet/tls"
	_ "github.com/xtls/xray-core/transport/internet/udp"
	_ "github.com/xtls/xray-core/transport/internet/websocket"
)
