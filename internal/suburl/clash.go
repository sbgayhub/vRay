package suburl

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/store"
	"gopkg.in/yaml.v2"
)

type clashProxy struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Server      string   `yaml:"server"`
	Port        int      `yaml:"port"`
	UUID        string   `yaml:"uuid"`
	Password    string   `yaml:"password"`
	Cipher      string   `yaml:"cipher"`
	TLS         bool     `yaml:"tls"`
	SkipCert    bool     `yaml:"skip-cert-verify"`
	ServerName  string   `yaml:"servername"`
	SNI         string   `yaml:"sni"`
	Fingerprint string   `yaml:"client-fingerprint"`
	Flow        string   `yaml:"flow"`
	Network     string   `yaml:"network"`
	ALPN        []string `yaml:"alpn"`
	WS          struct {
		Path    string            `yaml:"path"`
		Headers map[string]string `yaml:"headers"`
	} `yaml:"ws-opts"`
	GRPC struct {
		Service string `yaml:"grpc-service-name"`
	} `yaml:"grpc-opts"`
	Reality struct {
		PublicKey string `yaml:"public-key"`
		ShortID   string `yaml:"short-id"`
	} `yaml:"reality-opts"`
}

func parseClash(text string) Result {
	result := Result{Profiles: []store.Profile{}, Groups: []store.PolicyGroup{}, Diagnostics: []Diagnostic{}}
	var document struct {
		Proxies []clashProxy `yaml:"proxies"`
		Groups  []struct {
			Name     string   `yaml:"name"`
			Type     string   `yaml:"type"`
			Members  []string `yaml:"proxies"`
			URL      string   `yaml:"url"`
			Interval int      `yaml:"interval"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.UnmarshalStrict([]byte(text), &document); err != nil {
		// 完整的 Clash 配置包含许多其他顶层选项。解析支持的节点和策略组部分，
		// 忽略这些选项。
		if err := yaml.Unmarshal([]byte(text), &document); err != nil {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Field: "format", Message: "Clash 订阅格式无效"})
			return result
		}
	}
	for i, p := range document.Proxies {
		protocol := strings.ToLower(p.Type)
		if protocol == "ss" {
			protocol = "shadowsocks"
		}
		if protocol == "hy2" {
			protocol = "hysteria2"
		}
		switch protocol {
		case "vless", "vmess", "trojan", "shadowsocks", "hysteria2":
		default:
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Line: i + 1, Field: "protocol", Message: fmt.Sprintf("不支持的协议 %q", protocol)})
			continue
		}
		secret := p.Password
		if protocol == "vless" || protocol == "vmess" {
			secret = p.UUID
		}
		if p.Name == "" || p.Server == "" || p.Port < 1 || p.Port > 65535 || secret == "" {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Line: i + 1, Field: "node", Message: "节点缺少名称、服务器、端口或凭据"})
			continue
		}
		o := configgen.Outbound{Name: p.Name, Protocol: protocol, Address: p.Server, Port: p.Port, Secret: secret,
			Method: p.Cipher, Network: p.Network, SNI: p.SNI, Fingerprint: p.Fingerprint, Flow: p.Flow,
			ALPN: strings.Join(p.ALPN, ","), AllowInsecure: p.SkipCert, Path: p.WS.Path, Host: p.WS.Headers["Host"], Service: p.GRPC.Service}
		if o.SNI == "" {
			o.SNI = p.ServerName
		}
		if p.TLS || protocol == "trojan" || protocol == "hysteria2" {
			o.Security = "tls"
		}
		if p.Reality.PublicKey != "" {
			o.Security = "reality"
			o.PublicKey = p.Reality.PublicKey
			o.ShortID = p.Reality.ShortID
		}
		raw, _ := json.Marshal(o)
		result.Profiles = append(result.Profiles, store.Profile{Name: p.Name, Protocol: store.Protocol(protocol), Address: p.Server, Port: p.Port, SecretRef: secret, RawOutbound: raw})
	}
	for _, g := range document.Groups {
		switch g.Type {
		case "select", "url-test", "fallback", "load-balance":
			if g.Name != "" && len(g.Members) > 0 {
				result.Groups = append(result.Groups, store.PolicyGroup{Name: g.Name, Type: g.Type, Members: g.Members, URL: g.URL, Interval: g.Interval})
			}
		default:
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Field: "group", Message: "不支持的策略组类型：" + g.Type})
		}
	}
	return result
}
