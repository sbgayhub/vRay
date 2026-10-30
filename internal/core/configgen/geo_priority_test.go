package configgen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	router "github.com/xtls/xray-core/common/geodata"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	routingSession "github.com/xtls/xray-core/features/routing/session"
	"google.golang.org/protobuf/proto"
)

func TestCustomBlockWinsOverDomesticGeo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("xray.location.asset", dir)
	domain := func(value string) *router.Domain { return &router.Domain{Type: router.Domain_Full, Value: value} }
	fixtures := map[string]proto.Message{
		"geosite.dat": &router.GeoSiteList{Entry: []*router.GeoSite{
			{Code: "CN", Domain: []*router.Domain{domain("ads.example.cn"), domain("news.example.cn")}},
			{Code: "CATEGORY-ADS-ALL", Domain: []*router.Domain{domain("ads.example.cn")}},
		}},
		"geoip.dat": &router.GeoIPList{Entry: []*router.GeoIP{
			{Code: "PRIVATE", Cidr: []*router.CIDR{{Ip: []byte{10, 0, 0, 0}, Prefix: 8}}},
		}},
	}
	for name, message := range fixtures {
		raw, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	in := withGeo(systemProxyInput())
	in.Settings.DNSServers, in.Settings.DNSPort = nil, 0
	in.Rules = []Rule{{ID: "ads", Domains: []string{"geosite:category-ads-all"}, Outbound: TagBlock}}
	raw, _, err := Build(in)
	if err != nil {
		t.Fatal(err)
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
	routerFeature := instance.GetFeature(routing.RouterType()).(routing.Router)
	for _, tc := range []struct{ address, want string }{
		{"ads.example.cn", TagBlock}, {"news.example.cn", TagDirect}, {"10.1.2.3", TagDirect},
	} {
		ctx := &routingSession.Context{Outbound: &session.Outbound{Target: xnet.TCPDestination(xnet.ParseAddress(tc.address), 443)}}
		route, err := routerFeature.PickRoute(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if route.GetOutboundTag() != tc.want {
			t.Fatalf("%s routed to %s, want %s", tc.address, route.GetOutboundTag(), tc.want)
		}
	}
}

func TestMissingGeoSkipsWholeRuleAndKeepsOrdinaryRules(t *testing.T) {
	in := systemProxyInput()
	in.Rules = append(in.Rules, Rule{ID: "geo-ads", Domains: []string{"geosite:category-ads-all"}, Ports: "443", Outbound: TagBlock})
	raw, diags, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 1 || diags[0].Severity != SeverityWarning {
		t.Fatalf("missing Geo diagnostic: %+v", diags)
	}
	if bytes.Contains(raw, []byte("rule-geo-ads")) || !bytes.Contains(raw, []byte("rule-ads")) {
		t.Fatal("missing data removed ordinary rules or broadened Geo rule")
	}
	if _, err := core.LoadConfig("json", bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
}
