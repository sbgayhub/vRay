package core

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/xtls/xray-core/core"
)

func TestExplicitInsecureNodeMigratesToCachedCertificatePin(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	host, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	e := New()
	node := configgen.Outbound{Protocol: "trojan", Address: host, Port: p, Secret: "password", AllowInsecure: true}
	prepared, err := e.PrepareNode(t.Context(), node)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.PinnedPeerCertSHA256) != 64 {
		t.Fatal("certificate was not pinned")
	}
	raw, err := configgen.BuildNodeDialer(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"allowInsecure"`)) {
		t.Fatal("removed TLS option was emitted")
	}
	if _, err := core.LoadConfig("json", bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	server.Close()
	again, err := e.PrepareNode(t.Context(), node)
	if err != nil || again.PinnedPeerCertSHA256 != prepared.PinnedPeerCertSHA256 {
		t.Fatal("pin was not reused")
	}
	verified := configgen.Outbound{Protocol: "vless", Address: "must-not-resolve.invalid", Port: 443, Secret: "uuid", Security: "tls"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	unchanged, err := e.PrepareNode(ctx, verified)
	if err != nil || unchanged.PinnedPeerCertSHA256 != "" || unchanged.AllowInsecure {
		t.Fatal("ordinary certificate verification was changed")
	}
}
