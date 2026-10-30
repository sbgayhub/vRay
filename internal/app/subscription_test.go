package app

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// subscriptionServer 提供订阅正文，并带有真实面板会发送的流量头。
func subscriptionServer(t *testing.T, body string, hdr http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("the fetch sent no User-Agent")
		}
		for k, vs := range hdr {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// waitImport 轮询直到导入框的最后结果不同于 prev，从而避免测试与 goroutine 竞态。
func waitImport(t *testing.T, svc *Service, prev uint64) Snapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sn := svc.State().Snapshot()
		if sn.Version != prev && (sn.ImportAdded > 0 || sn.ImportProblem != "") {
			return sn
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("import never finished: %+v", svc.State().Snapshot())
	return Snapshot{}
}

// 订阅 URL 会被获取并导入。
//
// 这是用户报告“粘贴 https 链接没有反应”的场景：导入路径过去只解析文本，因此 URL 会被回答为 "not a share link"，且根本不会发起请求。
func TestImportFetchesSubscriptionURL(t *testing.T) {
	body := "vless://11111111-2222-3333-4444-555555555555@jp.example.com:443?security=tls#A\n" +
		"trojan://pass@tr.example.com:443#B\n"
	srv := subscriptionServer(t, body, http.Header{
		"Subscription-Userinfo": {"upload=1000; download=2000; total=5000; expire=1900000000"},
	})

	svc, _ := newTestService(t)
	before := svc.State().Snapshot().Version
	svc.Import(ImportInput{Text: srv.URL})
	sn := waitImport(t, svc, before)

	if sn.ImportProblem != "" {
		t.Fatalf("import problem: %s", sn.ImportProblem)
	}
	if sn.ImportAdded != 2 {
		t.Fatalf("added = %d, want 2", sn.ImportAdded)
	}
	if len(sn.Profiles) != 2 {
		t.Fatalf("profiles = %d", len(sn.Profiles))
	}
	// 订阅会被存储，以便之后刷新。
	if len(sn.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(sn.Sources))
	}
	if !sn.Sources[0].Enabled {
		t.Fatal("the stored subscription is disabled")
	}
	if sn.Sources[0].NodeCount != 2 {
		t.Fatalf("node count = %d, want 2", sn.Sources[0].NodeCount)
	}
}

// 流量头会保留，因为它是唯一说明用户剩余配额的字段。
func TestImportStoresSubscriptionTraffic(t *testing.T) {
	srv := subscriptionServer(t,
		"vless://11111111-2222-3333-4444-555555555555@jp.example.com:443#A\n",
		http.Header{"Subscription-Userinfo": {"upload=1000; download=2000; total=5000; expire=1900000000"}})

	svc, _ := newTestService(t)
	before := svc.State().Snapshot().Version
	svc.Import(ImportInput{Text: srv.URL})
	waitImport(t, svc, before)

	d, err := svc.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(d.Sources) != 1 {
		t.Fatalf("sources = %d", len(d.Sources))
	}
	src := d.Sources[0]
	if src.Upload != 1000 || src.Download != 2000 || src.Total != 5000 {
		t.Fatalf("traffic = %d/%d/%d", src.Upload, src.Download, src.Total)
	}
	if src.ExpireAt.IsZero() {
		t.Fatal("the expiry was not stored")
	}
	if src.LastFetchedAt.IsZero() {
		t.Fatal("the fetch time was not stored")
	}
	// URL 本身保存在密钥 keeper 中，而不是数据库里。
	if src.URLRef == "" {
		t.Fatal("the subscription has no URL reference")
	}
	if strings.Contains(src.URLRef, "://") {
		t.Fatal("the database holds the URL itself instead of a reference")
	}
	got, err := svc.secrets.Get(src.URLRef)
	if err != nil {
		t.Fatalf("the subscription URL is not in the secret keeper: %v", err)
	}
	if string(got) != srv.URL {
		t.Fatalf("stored URL = %q, want %q", got, srv.URL)
	}
}

// base64 订阅正文会被解码，这是大多数面板发送的格式。
func TestImportDecodesBase64Subscription(t *testing.T) {
	plain := "vless://11111111-2222-3333-4444-555555555555@jp.example.com:443#A\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))
	srv := subscriptionServer(t, encoded, nil)

	svc, _ := newTestService(t)
	before := svc.State().Snapshot().Version
	svc.Import(ImportInput{Text: srv.URL})
	sn := waitImport(t, svc, before)
	if sn.ImportAdded != 1 {
		t.Fatalf("added = %d, problem = %q", sn.ImportAdded, sn.ImportProblem)
	}
}

// 服务器错误会用文字报告，不会被静默忽略。
func TestImportReportsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer srv.Close()

	svc, _ := newTestService(t)
	before := svc.State().Snapshot().Version
	svc.Import(ImportInput{Text: srv.URL})
	sn := waitImport(t, svc, before)
	if sn.ImportProblem == "" {
		t.Fatal("a 403 was reported as success")
	}
	if !strings.Contains(sn.ImportProblem, "403") {
		t.Fatalf("problem = %q, want it to mention the status", sn.ImportProblem)
	}
	if len(sn.Profiles) != 0 {
		t.Fatal("profiles were stored despite the failure")
	}
}

// 无法访问的主机会被报告，而不是一直挂起。
func TestImportReportsUnreachableHost(t *testing.T) {
	svc, _ := newTestService(t)
	before := svc.State().Snapshot().Version
	// 回环地址上无人监听的端口。
	svc.Import(ImportInput{Text: "http://127.0.0.1:1/sub"})
	sn := waitImport(t, svc, before)
	if sn.ImportProblem == "" {
		t.Fatal("an unreachable host was reported as success")
	}
}

// 粘贴的分享链接仍然有效：URL 分支不能吞掉它们。
func TestImportStillParsesPastedLinks(t *testing.T) {
	svc, _ := newTestService(t)
	before := svc.State().Snapshot().Version
	svc.Import(ImportInput{
		Text: "vless://11111111-2222-3333-4444-555555555555@jp.example.com:443#A",
	})
	sn := waitImport(t, svc, before)
	if sn.ImportAdded != 1 {
		t.Fatalf("added = %d, problem = %q", sn.ImportAdded, sn.ImportProblem)
	}
	if len(sn.Sources) != 0 {
		t.Fatal("a pasted link created a subscription")
	}
}

// 粘贴多个链接时仍是链接，即使其中一行看起来像 URL。
func TestLooksLikeSubscriptionURL(t *testing.T) {
	cases := map[string]bool{
		"https://panel.example.com/sub?token=x":    true,
		"http://panel.example.com/sub":             true,
		"  https://panel.example.com/sub  ":        true,
		"vless://uuid@host:443":                    false,
		"trojan://pass@host:443":                   false,
		"hysteria2://auth@host:443":                false,
		"https://a.com/sub\nvless://uuid@host:443": false,
		"not a url":                   false,
		"":                            false,
		"ftp://panel.example.com/sub": false,
		"file:///etc/passwd":          false,
	}
	for in, want := range cases {
		if got := looksLikeSubscriptionURL(in); got != want {
			t.Fatalf("looksLikeSubscriptionURL(%q) = %v, want %v", in, got, want)
		}
	}
}

// 订阅显示名来自主机名，绝不能使用 token。
func TestSubscriptionName(t *testing.T) {
	cases := map[string]string{
		"https://panel.example.com/api/v1/client/subscribe?token=secret": "panel",
		"https://www.panel.example.com/sub?token=secret":                 "panel",
		"http://127.0.0.1:8080/sub":                                      "127",
		"":                                                               "订阅",
	}
	for in, want := range cases {
		if got := subscriptionName(in); got != want {
			t.Fatalf("subscriptionName(%q) = %q, want %q", in, got, want)
		}
	}
}
