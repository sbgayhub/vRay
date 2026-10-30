package core

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	xlog "github.com/xtls/xray-core/app/log"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"

	"github.com/sbgayhub/vray/internal/core/configgen"
)

var inspectionSequence atomic.Uint64

// inspectionOutbound 在活动实例中没有流量计数器，只能通过其唯一的强制标签访问。
// 关闭操作与 Xray 自身的关闭流程共用，因此检查请求仍在进行时也可能发生关闭。
type inspectionOutbound struct {
	outbound.Handler
	tag  string
	once sync.Once
}

func (h *inspectionOutbound) Tag() string { return h.tag }
func (h *inspectionOutbound) Close() error {
	var err error
	h.once.Do(func() { err = h.Handler.Close() })
	return err
}

// NodeHTTPClient 通过这个确切节点发送 HTTP 请求，不受当前路由规则和环境代理影响。
// 请求结束后必须运行 cleanup。
func (e *Engine) NodeHTTPClient(ctx context.Context, node configgen.Outbound) (client *http.Client, cleanup func(), err error) {
	var failureCleanup func()
	defer func() {
		if recover() != nil {
			if failureCleanup != nil {
				failureCleanup()
			}
			client, cleanup, err = nil, nil, fmt.Errorf("节点探测内核启动失败")
		}
	}()
	node, err = e.PrepareNode(ctx, node)
	if err != nil {
		return nil, nil, err
	}
	raw, err := configgen.BuildNodeDialer(node)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := core.LoadConfig("json", bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("节点探测配置无效")
	}
	// 加载 JSON 会插入进程级日志记录器。切勿在此处实例化该日志记录器：
	// 否则会替换活动核心的收集器和级别设置。
	loggerType := string((&xlog.Config{}).ProtoReflect().Descriptor().FullName())
	apps := cfg.App[:0]
	for _, app := range cfg.App {
		if app.Type != loggerType {
			apps = append(apps, app)
		}
	}
	cfg.App = apps
	proxyConfig := cfg.Outbound[0]
	cfg.Outbound = nil

	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	inst := e.inst
	temporary := inst == nil
	if temporary {
		// 空闲宿主只有 dispatcher/managers，没有监听器、日志模块、
		// 用户路由或运行中的引擎状态。已连接时应复用真实宿主：
		// core.New 会替换 Xray 的全局 DNS/dialer 绑定。
		inst, err = core.NewWithContext(ctx, cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("无法创建节点探测宿主")
		}
	}
	closeHost := func() {
		if temporary {
			_ = inst.Close()
		}
	}
	failureCleanup = closeHost
	object, err := core.CreateObject(inst, proxyConfig)
	if err != nil {
		closeHost()
		return nil, nil, fmt.Errorf("无法创建节点探测出站")
	}
	handler := &inspectionOutbound{Handler: object.(outbound.Handler), tag: fmt.Sprintf("ip-inspection:%d", inspectionSequence.Add(1))}
	failureCleanup = func() { _ = handler.Close(); closeHost() }
	manager := inst.GetFeature(outbound.ManagerType()).(outbound.Manager)
	if err := manager.AddHandler(ctx, handler); err != nil {
		_ = handler.Close()
		closeHost()
		return nil, nil, fmt.Errorf("无法注册节点探测出站")
	}
	removeOutbound := func() {
		// 在固定版本的 Xray 中，RemoveHandler 不会关闭 handler。
		_ = manager.RemoveHandler(context.WithoutCancel(ctx), handler.tag)
		_ = handler.Close()
		closeHost()
	}
	failureCleanup = removeOutbound
	lookupContext, cancel := context.WithCancel(ctx)
	transport := &http.Transport{
		// Proxy 有意保持为 nil；ProxyFromEnvironment 可能将此请求发送到
		// 当前系统代理，而不是所选节点。
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		DialContext: func(dialContext context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" && network != "tcp4" && network != "tcp6" {
				return nil, fmt.Errorf("节点探测仅支持 TCP")
			}
			if err := lookupContext.Err(); err != nil {
				return nil, err
			}
			destination, err := xnet.ParseDestination("tcp:" + address)
			if err != nil {
				return nil, err
			}
			connectionContext, stop := context.WithCancel(dialContext)
			stopParent := context.AfterFunc(lookupContext, stop)
			connectionContext = session.SetForcedOutboundTagToContext(connectionContext, handler.tag)
			conn, err := core.Dial(connectionContext, inst, destination)
			if err != nil {
				stopParent()
				stop()
				return nil, err
			}
			var closeOnce sync.Once
			var closeError error
			closePipe := func() error {
				closeOnce.Do(func() { closeError = conn.Close() })
				return closeError
			}
			stopClose := context.AfterFunc(connectionContext, func() { _ = closePipe() })
			owned := &inspectionConnection{Conn: conn, cancel: stop, stopParent: stopParent, stopClose: stopClose, closePipe: closePipe}
			return owned, nil
		},
	}
	var once sync.Once
	cleanup = func() {
		once.Do(func() {
			cancel()
			transport.CloseIdleConnections()
			removeOutbound()
		})
	}
	failureCleanup = cleanup
	client = &http.Client{
		Transport:     transport,
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return client, cleanup, nil
}

type inspectionConnection struct {
	net.Conn
	cancel                context.CancelFunc
	stopParent, stopClose func() bool
	closePipe             func() error
	once                  sync.Once
}

func (c *inspectionConnection) Close() error {
	var err error
	c.once.Do(func() {
		if c.stopClose != nil {
			c.stopClose()
		}
		c.stopParent()
		c.cancel()
		err = c.closePipe()
	})
	return err
}
