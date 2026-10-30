package configgen

import (
	"bytes"
	"testing"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	routingSession "github.com/xtls/xray-core/features/routing/session"
)

func TestRoutingModesUseRealRouterAndPreserveLocalRoutes(t *testing.T) {
	for _, mode := range []string{"", "rule", "global", "direct"} {
		t.Run(mode, func(t *testing.T) {
			in := systemProxyInput()
			in.Settings.RoutingMode = mode
			in.Rules = append(in.Rules, Rule{
				ID: "special", Domains: []string{"full:special.example"}, Outbound: TagDirect,
			})
			raw, _, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "direct" && bytes.Contains(raw, []byte("observatory")) {
				t.Fatal("direct routing still probes the proxy in the background")
			}
			cfg, err := core.LoadConfig("json", bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			instance, err := core.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer instance.Close()
			picker := instance.GetFeature(routing.RouterType()).(routing.Router)
			ads, special := TagBlock, TagDirect
			if mode == "global" {
				ads, special = TagProxy, TagProxy
			} else if mode == "direct" {
				ads = TagDirect
			}
			for _, tc := range []struct{ host, inbound, want string }{
				{"ads.example.com", TagInSocks, ads},
				{"special.example", TagInSocks, special},
				{"10.1.2.3", TagInSocks, TagDirect},
				{"8.8.8.8", TagInAPI, TagAPI},
				{"8.8.8.8", TagInDNS, TagDirect},
			} {
				ctx := &routingSession.Context{
					Inbound: &session.Inbound{Tag: tc.inbound},
					Outbound: &session.Outbound{
						Target: xnet.TCPDestination(xnet.ParseAddress(tc.host), 443),
					},
				}
				route, err := picker.PickRoute(ctx)
				if err != nil {
					t.Fatalf("%s: %v", tc.host, err)
				}
				if got := route.GetOutboundTag(); got != tc.want {
					t.Fatalf("%s/%s routed to %s, want %s", tc.inbound, tc.host, got, tc.want)
				}
			}
		})
	}
}

func TestRoutingModeRejectsUnknownValue(t *testing.T) {
	in := systemProxyInput()
	in.Settings.RoutingMode = "other"
	if _, _, err := Build(in); err == nil {
		t.Fatal("unknown mode was accepted")
	}
}
