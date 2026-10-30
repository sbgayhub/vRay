package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/core/logs"
	"github.com/sbgayhub/vray/internal/store"
	"github.com/sbgayhub/vray/internal/suburl"
)

// 需显式启用：私有订阅内容和地址不会进入测试输出，凭据只留在内存中，且只获取少量数据。
func TestLiveSubscriptionExitAndDownload(t *testing.T) {
	link := os.Getenv("VRAY_TEST_SUBSCRIPTION")
	if link == "" {
		t.Skip("set VRAY_TEST_SUBSCRIPTION for the live diagnostic")
	}
	svc, _ := newTestService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	body, _, _, err := svc.fetchSubscription(ctx, link)
	if err != nil {
		t.Fatal("subscription fetch failed (URL omitted)")
	}
	parsed := suburl.ParseText(string(body))
	if len(parsed.Groups) == 0 {
		t.Fatal("the negotiated subscription did not include policy groups")
	}
	var profile store.Profile
	for _, candidate := range parsed.Profiles {
		if candidate.Protocol == "vless" {
			profile = candidate
			break
		}
	}
	if profile.Name == "" {
		t.Fatal("subscription has no VLESS test candidate")
	}
	var node configgen.Outbound
	if err := json.Unmarshal(profile.RawOutbound, &node); err != nil {
		t.Fatal("cannot decode candidate")
	}
	secret := node.Secret
	node.Secret = ""
	profile.RawOutbound, _ = json.Marshal(node)
	profile.ID = "remote"
	if err := svc.AddTestProfile(profile, secret); err != nil {
		t.Fatal("cannot prepare candidate")
	}
	if err := svc.Load(); err != nil {
		t.Fatal("cannot load candidate")
	}
	engine := xcore.New()
	defer engine.Stop()
	collector, _ := logs.NewCollector(logs.DefaultRing, "")
	defer collector.Close()
	collector.Attach()
	svc.engine = engine
	svc.downloadLimits = downloadLimits{MaxBytes: 64 << 10, Duration: 8 * time.Second, Timeout: 25 * time.Second}
	select {
	case <-svc.InspectNode("remote"):
	case <-time.After(30 * time.Second):
		t.Fatal("live lookup timed out")
	}
	info := svc.State().Snapshot().NodeDetails["remote"]
	if info.IP == "" || info.ASN == "" || info.CheckedAt.IsZero() || info.Error != "" {
		t.Fatalf("live exit lookup failed: %s", info.Error)
	}
	select {
	case <-svc.TestDownload("remote"):
	case <-time.After(30 * time.Second):
		t.Fatal("live download timed out")
	}
	speed := svc.State().Snapshot().NodeSpeeds["remote"]
	if speed.BytesPerSecond <= 0 || speed.CheckedAt.IsZero() || speed.Error != "" {
		t.Fatalf("live download failed: %s", speed.Error)
	}
	if err := svc.Load(); err != nil {
		t.Fatal("could not reload persisted observations")
	}
	snap := svc.State().Snapshot()
	if snap.NodeDetails["remote"].IP != info.IP || snap.NodeSpeeds["remote"].BytesPerSecond != speed.BytesPerSecond {
		t.Fatal("live observations did not survive reload")
	}
	t.Logf("parsed nodes=%d groups=%d; exit/ASN lookup and download succeeded, both persisted", len(parsed.Profiles), len(parsed.Groups))
}
