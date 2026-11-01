// Command subcheck 在内存中诊断显式提供的订阅。它绝不打印链接、凭据、节点地址
// 或订阅内容。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/diag"
	"github.com/sbgayhub/vray/internal/suburl"
	xlog "github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/core"
	"gopkg.in/yaml.v2"
)

type quietLog struct{}

func (quietLog) Handle(xlog.Message) {}

func main() {
	xlog.RegisterHandler(quietLog{})
	rawURL := os.Getenv("VRAY_TEST_SUBSCRIPTION")
	if rawURL == "" {
		fmt.Println("VRAY_TEST_SUBSCRIPTION is required")
		os.Exit(1)
	}
	client := &http.Client{Timeout: 25 * time.Second}
	for _, agent := range []string{"vRay/1.0 (Xray client)", "vRay/1.0 (Xray client; Clash.Meta)"} {
		req, _ := http.NewRequest(http.MethodGet, rawURL, nil)
		req.Header.Set("User-Agent", agent)
		resp, err := client.Do(req)
		if err != nil {
			fmt.Println("subscription fetch failed; URL omitted")
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if readErr != nil || resp.StatusCode != 200 {
			fmt.Printf("subscription response status=%d readOK=%t\n", resp.StatusCode, readErr == nil)
			continue
		}
		if strings.Contains(agent, "Clash.Meta") {
			var config struct {
				Proxies []struct {
					Type string `yaml:"type"`
					Obfs string `yaml:"obfs"`
				} `yaml:"proxies"`
				Groups []struct {
					Type    string   `yaml:"type"`
					Proxies []string `yaml:"proxies"`
				} `yaml:"proxy-groups"`
				Rules []string `yaml:"rules"`
			}
			if yaml.Unmarshal(body, &config) == nil {
				types := map[string]int{}
				obfuscated := 0
				for _, proxy := range config.Proxies {
					if proxy.Obfs != "" {
						obfuscated++
					}
				}
				for _, g := range config.Groups {
					types[g.Type]++
				}
				fmt.Printf("clash nodes=%d groups=%d groupTypes=%v rules=%d obfuscatedNodes=%d\n", len(config.Proxies), len(config.Groups), types, len(config.Rules), obfuscated)
				parsed := suburl.ParseText(string(body))
				fmt.Printf("clash import parsedNodes=%d parsedGroups=%d diagnostics=%d\n", len(parsed.Profiles), len(parsed.Groups), len(parsed.Diagnostics))
			}
			continue
		}
		parsed := suburl.ParseText(string(body))
		fmt.Printf("link nodes=%d diagnostics=%d\n", len(parsed.Profiles), len(parsed.Diagnostics))
		seen := map[string]bool{}
		for i, p := range parsed.Profiles {
			if seen[string(p.Protocol)] {
				continue
			}
			seen[string(p.Protocol)] = true
			var node configgen.Outbound
			_ = json.Unmarshal(p.RawOutbound, &node)
			if raw, configErr := configgen.BuildNodeDialer(node); configErr == nil {
				if _, loadErr := core.LoadConfig("json", bytes.NewReader(raw)); loadErr != nil {
					detail := diag.Scrub(loadErr.Error())
					if node.Secret != "" {
						detail = strings.ReplaceAll(detail, node.Secret, "[redacted]")
					}
					fmt.Printf("node=%d configError=%s\n", i+1, detail)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			e := xcore.New()
			httpClient, cleanup, err := e.NodeHTTPClient(ctx, node)
			if err == nil {
				r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.cloudflare.com/cdn-cgi/trace", nil)
				response, requestErr := httpClient.Do(r)
				err = requestErr
				if response != nil {
					_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
					_ = response.Body.Close()
					fmt.Printf("node=%d protocol=%s security=%s network=%s tlsSkip=%t status=%d\n", i+1, p.Protocol, node.Security, node.Network, node.AllowInsecure, response.StatusCode)
					if err == nil && response.StatusCode == 200 {
						for _, target := range []struct{ label, url string }{
							{"exit-info", "https://api.ipapi.is/"},
							{"exit-fallback", "https://ipwho.is/"},
							{"download", "https://speed.cloudflare.com/__down?bytes=65536"},
						} {
							r, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.url, nil)
							if target.label == "exit-info" {
								r, _ = http.NewRequestWithContext(ctx, http.MethodPost, target.url, strings.NewReader("{}"))
								r.Header.Set("Content-Type", "application/json")
							}
							resp, requestErr := httpClient.Do(r)
							if requestErr != nil {
								cause := requestErr.Error()
								for _, sensitive := range []string{node.Address, node.Secret, node.SNI, node.Host} {
									if sensitive != "" {
										cause = strings.ReplaceAll(cause, sensitive, "[redacted]")
									}
								}
								fmt.Printf("node=%d check=%s failure=%s\n", i+1, target.label, diag.Scrub(cause))
								continue
							}
							n, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
							_ = resp.Body.Close()
							fmt.Printf("node=%d check=%s status=%d bytes=%d readOK=%t\n", i+1, target.label, resp.StatusCode, n, readErr == nil)
						}
					}
				}
				cleanup()
			}
			if err != nil {
				kind := "transport"
				for _, key := range []string{"certificate", "timeout", "deadline", "EOF", "configuration", "outbound"} {
					if strings.Contains(err.Error(), key) {
						kind = key
						break
					}
				}
				fmt.Printf("node=%d protocol=%s requestFailed=%s\n", i+1, p.Protocol, kind)
			}
			cancel()
		}
	}
}
