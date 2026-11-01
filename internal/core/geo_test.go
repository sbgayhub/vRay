package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	xcore "github.com/xtls/xray-core/core"

	"github.com/sbgayhub/vray/internal/paths"
)

// assetLocationEnv 是 Xray 用于指定数据文件目录的变量。
// 有意在此重复定义：测试不能读取 app 的常量，这样名称变化
// 才会暴露出来，而不是被静默同步。
const assetLocationEnv = "xray.location.asset"

// geoConfig 是一个按 geoip: 和 geosite: 路由的最小配置。
const geoConfig = `{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "tag": "in-socks",
    "listen": "127.0.0.1",
    "port": 0,
    "protocol": "mixed",
    "settings": {"auth": "noauth"}
  }],
  "outbounds": [
    {"tag": "proxy", "protocol": "freedom", "settings": {}},
    {"tag": "direct", "protocol": "freedom", "settings": {}},
    {"tag": "block", "protocol": "blackhole", "settings": {}}
  ],
  "routing": {
    "domainStrategy": "AsIs",
    "rules": [
      {"type": "field", "ruleTag": "r1", "ip": ["geoip:private"], "outboundTag": "direct"},
      {"type": "field", "ruleTag": "r2", "domain": ["geosite:category-ads-all"], "outboundTag": "block"},
      {"type": "field", "ruleTag": "r3", "ip": ["geoip:cn"], "outboundTag": "direct"},
      {"type": "field", "ruleTag": "r4", "domain": ["geosite:cn"], "outboundTag": "direct"}
    ]
  }
}`

// geoDataDir 返回真实数据文件所在目录；若不存在则返回 ""。
//
// 优先使用 VRAY_GEODATA；否则使用模块旁的 ./data。这些文件共 26 MB
// 且运行时下载，因此需要它们的测试在文件缺失时应跳过而不是失败。
func geoDataDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("VRAY_GEODATA"); dir != "" {
		return dir
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "data"))
	if err != nil {
		return ""
	}
	if (paths.Paths{Root: root}).HasGeoData() {
		return root
	}
	return ""
}

// TestGeoRulesLoadWithAssetLocation 证明项目在实践中发现的真实依赖：
// 只有当 Xray 能找到 geoip.dat 和 geosite.dat 时，geoip: 和 geosite: 规则
// 才能工作，而 Xray 通过 xray.location.asset 查找它们。
func TestGeoRulesLoadWithAssetLocation(t *testing.T) {
	dir := geoDataDir(t)
	if dir == "" {
		t.Skip("geo data not present; set VRAY_GEODATA or run the app once with network access")
	}
	t.Setenv(assetLocationEnv, dir)

	if _, err := xcore.LoadConfig("json", bytes.NewReader([]byte(geoConfig))); err != nil {
		t.Fatalf("geo rules do not load even with %s set: %v", assetLocationEnv, err)
	}
}

// TestGeoRulesFailWithoutAsset 展示应用必须避免的失败模式：
// 如果该变量未指向这些文件，同一配置会被拒绝。
// 这就是 Service.prepareConfig 每次启动前都要设置它的原因。
func TestGeoRulesFailWithoutAsset(t *testing.T) {
	// 空目录可确保无论测试在哪里运行，查找都会失败。
	t.Setenv(assetLocationEnv, t.TempDir())

	if _, err := xcore.LoadConfig("json", bytes.NewReader([]byte(geoConfig))); err == nil {
		t.Fatal("expected geoip:private to fail without data files")
	}
}

// TestRealGeoRulesStartRealXray 比加载更进了一步：嵌入式核心
// 实际使用 geo 配置并设置资源目录后启动。
func TestRealGeoRulesStartRealXray(t *testing.T) {
	dir := geoDataDir(t)
	if dir == "" {
		t.Skip("geo data not present; set VRAY_GEODATA or run the app once with network access")
	}
	t.Setenv(assetLocationEnv, dir)

	eng := New()
	defer func() { _ = eng.Stop() }()

	if err := eng.Start([]byte(geoConfig)); err != nil {
		t.Fatalf("the real core rejected a geo config: %v", err)
	}
	if !eng.Running() {
		t.Fatal("engine reported not running after a successful start")
	}
}
