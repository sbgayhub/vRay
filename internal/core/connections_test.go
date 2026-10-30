package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	xray "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"

	"github.com/sbgayhub/vray/internal/core/configgen"
)

func waitConnections(t *testing.T, e *Engine, count int) []Connection {
	t.Helper()
	end := time.Now().Add(4 * time.Second)
	for time.Now().Before(end) {
		list := e.Connections()
		if len(list) == count {
			return list
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("connections = %+v, want %d", e.Connections(), count)
	return nil
}

func TestConnectionsFollowRealVLESSTraffic(t *testing.T) {
	for _, mux := range []bool{false, true} {
		name := "plain"
		if mux {
			name = "mux"
		}
		t.Run(name, func(t *testing.T) {
			payload := strings.Repeat("vless-stream", 4096)
			var chunks atomic.Int32
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != "upload-body" {
					t.Errorf("upstream body=%q err=%v", body, err)
				}
				for range 2 {
					_, _ = io.WriteString(w, payload)
					w.(http.Flusher).Flush()
					chunks.Add(1)
					select {
					case <-release:
						return
					case <-r.Context().Done():
						return
					case <-time.After(100 * time.Millisecond):
					}
				}
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer api.Close()
			apiURL, _ := url.Parse(api.URL)
			relayPort, port := freePort(t), freePort(t)
			const id = "11111111-2222-3333-4444-555555555555"
			serverConfig, _ := json.Marshal(map[string]any{
				"log":      map[string]any{"loglevel": "none"},
				"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": relayPort, "protocol": "vless", "settings": map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": id}}}}},
				"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{
					"redirect": apiURL.Host, "finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}},
				}}},
			})
			relay, err := xray.StartInstance("json", serverConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer relay.Close()
			node := configgen.Outbound{Protocol: "vless", Address: "127.0.0.1", Port: relayPort, Secret: id}
			if mux {
				node.Mux = &configgen.Mux{Enabled: true, Concurrency: 8}
			}
			cfg, _, err := configgen.Build(configgen.Input{Active: node, Mode: configgen.ModeSystemProxy, Settings: configgen.Settings{MixedPort: port, LogLevel: "none", ProbeURL: api.URL + "/health"}})
			if err != nil {
				t.Fatal(err)
			}
			e := New()
			defer e.Stop()
			if err := e.Start(cfg); err != nil {
				t.Fatal(err)
			}
			proxyURL, _ := url.Parse("http://127.0.0.1:" + fmt.Sprint(port))
			tr := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
			resp, err := client.Post("http://video-fixture.invalid/segment", "text/plain", strings.NewReader("upload-body"))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			first := make([]byte, len(payload))
			if _, err := io.ReadFull(resp.Body, first); err != nil || string(first) != payload {
				t.Fatalf("first chunk err=%v", err)
			}
			list := waitConnections(t, e, 1)
			if list[0].Destination != "video-fixture.invalid:80" || list[0].Outbound != "proxy" || list[0].Upload < int64(len("upload-body")) || list[0].Download < int64(len(payload)) || list[0].Source == "" {
				t.Fatalf("bad live connection: %+v", list[0])
			}
			before := list[0].Download
			if _, err := io.ReadFull(resp.Body, first); err != nil {
				t.Fatal(err)
			}
			list = e.Connections()
			if len(list) != 1 || list[0].Download <= before {
				t.Fatalf("active counter did not grow: %+v, before=%d", list, before)
			}
			list[0].Destination = "mutation"
			if e.Connections()[0].Destination == "mutation" {
				t.Fatal("snapshot aliases connection state")
			}
			releaseOnce.Do(func() { close(release) })
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			waitConnections(t, e, 0)
		})
	}
}

func TestConnectionsTrackUDPAndRemoveOnStop(t *testing.T) {
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		var b [128]byte
		for {
			n, addr, err := echo.ReadFrom(b[:])
			if err != nil {
				return
			}
			_, _ = echo.WriteTo(b[:n], addr)
		}
	}()
	defer func() { _ = echo.Close(); <-done }()
	port := freePort(t)
	cfg, _ := json.Marshal(map[string]any{
		"log":       map[string]any{"loglevel": "none"},
		"inbounds":  []any{map[string]any{"tag": "in-tun", "listen": "127.0.0.1", "port": port, "protocol": "dokodemo-door", "settings": map[string]any{"address": "127.0.0.1", "port": echo.LocalAddr().(*net.UDPAddr).Port, "network": "udp"}}},
		"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}},
	})
	e := New()
	defer e.Stop()
	if err := e.Start(cfg); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte("udp-payload"))
	var packet [128]byte
	n, err := conn.Read(packet[:])
	if err != nil || string(packet[:n]) != "udp-payload" {
		t.Fatalf("udp echo: %q, %v", packet[:n], err)
	}
	list := waitConnections(t, e, 1)
	if !strings.EqualFold(list[0].Network, "udp") || list[0].Upload != 11 || list[0].Download != 11 || list[0].Outbound != "direct" {
		t.Fatalf("udp accounting: %+v", list)
	}
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
	waitConnections(t, e, 0)
}

func TestConnectionTrackerExcludesInternalTrafficAndOldEpoch(t *testing.T) {
	tk := &connectionTracker{}
	epoch := tk.reset()
	dest := xnet.TCPDestination(xnet.DomainAddress("test.invalid"), 443)
	for _, tag := range []string{"", "in-api", "in-dns"} {
		ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: tag})
		ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: dest}})
		if tk.begin(ctx, epoch, "proxy") != nil {
			t.Fatalf("internal traffic %q tracked", tag)
		}
	}
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: "in-socks"})
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: dest}})
	old := tk.begin(ctx, epoch, "proxy")
	if old == nil {
		t.Fatal("normal traffic ignored")
	}
	current := tk.reset()
	if tk.begin(ctx, epoch, "proxy") != nil {
		t.Fatal("old core repopulated tracker")
	}
	fresh := tk.begin(ctx, current, "direct")
	tk.finish(old.info.ID)
	if len(tk.active) != 1 {
		t.Fatal("old cleanup removed a new connection")
	}
	tk.finish(fresh.info.ID)
	if tk.active != nil {
		t.Fatal("completed sessions retained map storage")
	}
}

type accountingHandler struct {
	outbound.Handler
	run func(*transport.Link)
}

func (h accountingHandler) Tag() string                                      { return "proxy" }
func (h accountingHandler) Dispatch(_ context.Context, link *transport.Link) { h.run(link) }

func TestConnectionWriterPreservesVisionAndUserCounter(t *testing.T) {
	tk := &connectionTracker{}
	epoch := tk.reset()
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: "in-socks"})
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: xnet.TCPDestination(xnet.DomainAddress("test.invalid"), 443)}})
	var userCounter connectionCounter
	original := &dispatcher.SizeStatWriter{Counter: &userCounter, Writer: buf.Discard}
	h := &connectionHandler{tracker: tk, epoch: epoch, Handler: accountingHandler{run: func(link *transport.Link) {
		// Vision 的直接复制路径会识别这个具体写入器类型。
		writer, ok := link.Writer.(*dispatcher.SizeStatWriter)
		if !ok {
			t.Fatal("Vision counter hook hidden")
		}
		writer.Counter.Add(2048)
		mb := buf.MultiBuffer{buf.FromBytes([]byte("abc"))}
		if err := writer.WriteMultiBuffer(mb); err != nil {
			t.Fatal(err)
		}
		if userCounter.Value() != 2051 {
			t.Fatalf("existing user counter lost: %d", userCounter.Value())
		}
		e := &Engine{connections: tk}
		if live := e.Connections(); len(live) != 1 || live[0].Download != 2051 {
			t.Fatalf("live counter wrong: %+v", live)
		}
	}}}
	h.Dispatch(ctx, &transport.Link{Reader: &buf.SingleReader{Reader: strings.NewReader("")}, Writer: original})
	if len(tk.active) != 0 {
		t.Fatal("completed dispatch retained")
	}
}

func TestConnectionsRemainDuringTCPHalfClose(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	release, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		upload := make([]byte, len("upload-before-half-close"))
		if _, err := io.ReadFull(conn, upload); err != nil {
			return
		}
		_, _ = io.WriteString(conn, "half-close-download")
		<-release
	}()
	defer func() { once.Do(func() { close(release) }); _ = l.Close(); <-done }()
	port := freePort(t)
	e := New()
	defer e.Stop()
	if err := e.Start(proxyConfig(port)); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTCP("tcp", nil, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", l.Addr(), l.Addr())
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	_, _ = io.WriteString(conn, "upload-before-half-close")
	_ = conn.CloseWrite()
	data := make([]byte, len("half-close-download"))
	if _, err := io.ReadFull(reader, data); err != nil || string(data) != "half-close-download" {
		t.Fatalf("half-close data=%q err=%v", data, err)
	}
	live := waitConnections(t, e, 1)
	if live[0].Download != int64(len(data)) {
		t.Fatalf("half-close accounting: %+v", live)
	}
	once.Do(func() { close(release) })
	_, _ = io.Copy(io.Discard, reader)
	_ = conn.Close()
	waitConnections(t, e, 0)
}
