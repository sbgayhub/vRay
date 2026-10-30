package logs

import (
	"strings"
	"testing"

	"github.com/xtls/xray-core/common/log"
)

func TestChineseDescriptionsPreserveTargets(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"proxy/socks: TCP Connect request to tcp:accepted.example.com:443", "TCP 连接请求"},
		{"app/dispatcher: taking detour [proxy] for [tcp:started.example.com:443]", "选择出站线路"},
		{"failed to dial tcp:failed.example.com:443 > connection refused", "连接被拒绝"},
		{"Xray 26.3.27 started", "已启动"},
		{"accepted tcp:rejected.example.com:443 [in-socks -> proxy]", "已接收连接"},
	} {
		got := Describe(tc.text)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("description = %s", got)
		}
		for _, target := range []string{"accepted.example.com", "started.example.com", "failed.example.com", "rejected.example.com"} {
			if strings.Contains(tc.text, target) && !strings.Contains(got, target) {
				t.Fatalf("target changed: %s", got)
			}
		}
	}
}

func TestNoneDisablesCollection(t *testing.T) {
	c, err := NewCollector(10, "")
	if err != nil {
		t.Fatal(err)
	}
	c.SetLevel(ParseLevel("none"))
	c.Handle(&log.GeneralMessage{Severity: log.Severity_Error, Content: "must not be collected"})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if len(c.Snapshot()) != 0 {
		t.Fatal("none still collects errors")
	}
}
