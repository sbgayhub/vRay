package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/core/apiclient"
	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/core/effects"
	"github.com/sbgayhub/vray/internal/core/logs"
	"github.com/sbgayhub/vray/internal/geo"
	"github.com/sbgayhub/vray/internal/paths"
	"github.com/sbgayhub/vray/internal/secrets"
	"github.com/sbgayhub/vray/internal/store"
	"github.com/sbgayhub/vray/internal/suburl"
)

// Engine 是服务所需的内核能力；真实实现是 internal/core.Engine，
// 测试用假实现替换。
type Engine interface {
	Start(cfg []byte) error
	Stop() error
	Running() bool
}

// LogSource 是日志页读取的日志缓冲。
type LogSource interface {
	// Snapshot 返回持有的日志行，最旧的在前。
	Snapshot() []logs.Entry
	// Clear 清空内存中的日志行；磁盘文件保留。
	Clear()
	// Dropped 返回因写入器落后而丢失的行数。
	Dropped() int64
}

// Service 是视图调用的对象。所有方法立即返回；工作在 goroutine 中执行，
// 结果写入 State。
type Service struct {
	policyCounterNodes map[string]string
	dashboardMu        sync.Mutex
	dashboardBusy      bool
	memoryRelease      chan struct{}
	dnsCancel          context.CancelFunc
	addresses          map[string]string
	noResolve          bool
	inspectCancel      context.CancelFunc
	inspectGeneration  uint64
	ipRetryAt          time.Time
	ipEndpoint         string
	nodeClient         nodeClientFactory
	downloadCancel     context.CancelFunc
	downloadGeneration uint64
	downloadEndpoint   string
	downloadLimits     downloadLimits
	updateMu           sync.Mutex
	updateBackend      UpdateBackend
	updateRelease      *UpdateRelease
	updateBusy         bool
	updateRootContext  context.Context
	updateScheduleOnce sync.Once
	lastAutoUpdateDate string
	postConsentOnce    sync.Once
	postConsentStart   func()
	dataRootBusy       atomic.Bool
	state              *State
	store              store.Store
	secrets            secrets.Keeper
	engine             Engine
	paths              paths.Paths

	// logs 非空时是日志页读取的缓冲。使用接口是为了让测试无需启动内核
	// 也能提供自己的实现。
	logs LogSource

	// mu 串行化连接/断开，避免两次点击交错。
	mu sync.Mutex
	// viewMu 只保护帧使用的指针，独立于 mu 下缓慢的内核操作。
	viewMu sync.RWMutex

	// apiPort 与 dnsPort 是本地辅助入站的端口。零值表示“连接时选一个
	// 空闲端口”；测试会显式设置。
	apiPort int
	dnsPort int

	// geo 下载 Xray 数据文件；不需要它的测试中为 nil。
	geo *geo.Downloader

	// stats 与 sampler 驱动流量图。stats 在客户端挂载前保持 nil，因此
	// 没有统计源的连接只显示零流量；sampler 每次连接重建，因为它的
	// 基线必须在每次内核启动时从零开始。
	stats   StatsSource
	sampler *Sampler
	// 只有本服务创建的客户端会随连接关闭。注入的计数源仍归调用方所有。
	statsClient  *apiclient.Client
	windowHidden atomic.Bool

	// noRealStats 阻止服务创建自己的 API 客户端。测试会设置它，
	// 因为假内核从不启动 API 入站。
	noRealStats bool

	// prober 测量节点延迟，供列表的测速按钮使用。首次使用时创建，
	// 因此从不测速的服务不会持有 goroutine。
	prober *Prober

	// effectsJournal 记录我们对系统所做的变更；需要前为 nil。
	effectsJournal *effects.Journal

	// systemProxy 与 dns 是平台层。使用接口是为了让测试无需改动本机
	// 设置也能驱动副作用日志。
	systemProxy EffectLayer
	dns         EffectLayer

	// loginSetter 应用开机自启选择；不需要它的测试中为 nil。
	loginSetter func(bool) error

	// httpClient 用于抓取订阅。nil 表示默认客户端；测试可指向自己的服务器。
	httpClient *http.Client
}

// EffectLayer 是一类可撤销的系统变更，例如系统代理或 DNS 设置。
//
// 服务从不执行日志中的命令字符串：变更通过对应类型的类型化方法还原，
// 因此缺陷或其他程序写入的日志文件无法让 vRay 执行任何命令。
type EffectLayer interface {
	// Restore 撤销一条已记录的变更。
	Restore(effects.Entry) error
}

// noEffect 是平台无事可做时使用的层。
type noEffect struct{}

// Restore 什么也不做。
func (noEffect) Restore(effects.Entry) error { return nil }

// New 在真实依赖之上构建服务。
func New(st *State, storer store.Store, keeper secrets.Keeper, engine Engine, p paths.Paths) *Service {
	return &Service{
		state: st, store: storer, secrets: keeper, engine: engine, paths: p,
		// 默认使用什么都不做的层：没有平台层时服务不能在还原时 panic，
		// 外壳会在首次连接前换成真实实现。
		systemProxy: noEffect{},
		dns:         noEffect{},
	}
}

// AttachEffectLayers 安装负责还原系统变更的平台层。
func (s *Service) AttachEffectLayers(systemProxy, dns EffectLayer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if systemProxy != nil {
		s.systemProxy = systemProxy
	}
	if dns != nil {
		s.dns = dns
	}
}

// AttachLoginSetter 安装应用开机自启选择的函数。
func (s *Service) AttachLoginSetter(fn func(bool) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loginSetter = fn
}

// State 暴露状态，供外壳挂接失效通知。
func (s *Service) State() *State { return s.state }

// runAsync 在后台 goroutine 中执行 fn，返回的通道在 fn 结束后关闭。
//
// 各个 SaveXxx/SetXxx 都以完成信号告知 UI 操作结束，这里是那份共用样板。
func (s *Service) runAsync(fn func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	return done
}

// Load 把数据库读入状态。启动时调用一次。
func (s *Service) Load() error {
	d, err := s.store.Load()
	if err != nil {
		return err
	}
	views := make([]ProfileView, 0, len(d.Profiles))
	names := make(map[string]string, len(d.Sources))
	for _, src := range d.Sources {
		names[src.ID] = src.Name
	}
	counts := make(map[string]int, len(d.Sources))
	for _, p := range d.Profiles {
		view := viewOf(p)
		s.viewMu.RLock()
		view.IP = s.addresses[p.Address]
		s.viewMu.RUnlock()
		view.SourceName = names[p.SourceID]
		if view.SourceName == "" {
			view.SourceName = "手动导入"
		}
		views = append(views, view)
		counts[p.SourceID]++
	}
	sources := make([]SourceView, 0, len(d.Sources))
	for _, src := range d.Sources {
		// 节点数量在这里统计，而不是读订阅记录：逐个删除过节点的订阅
		// 否则会继续声称拥有已不存在的节点。
		sources = append(sources, SourceView{
			Host:   src.Host,
			Upload: src.Upload, Download: src.Download, Total: src.Total,
			ExpireAt: src.ExpireAt, LastFetchedAt: src.LastFetchedAt,
			IntervalMinutes: src.IntervalMinutes,
			ID:              src.ID, Name: src.Name, Enabled: src.Enabled,
			NodeCount: counts[src.ID], LastError: src.LastError,
		})
	}

	consentRequired := d.Settings.TermsAcceptedVersion < store.LegalVersion
	s.state.Update(func(sn *Snapshot) {
		sn.Groups = append([]store.PolicyGroup(nil), d.Groups...)
		if sn.Usage == nil {
			sn.Usage = make(map[string]store.Transfer, len(d.Usage))
			for id, usage := range d.Usage {
				sn.Usage[id] = usage
			}
		}
		sn.Profiles = views
		details := make(map[string]IPDetails, len(d.NodeDetails))
		for _, view := range views {
			info, saved := d.NodeDetails[view.ID]
			previous, inSession := sn.NodeDetails[view.ID]
			if !saved && !inSession {
				continue
			}
			loaded := ipDetailsFromStore(info)
			loaded.Address, loaded.revision = view.Address, view.revision
			if previous.revision.Equal(view.revision) {
				loaded.Loading, loaded.Error, loaded.request = previous.Loading, previous.Error, previous.request
			}
			details[view.ID] = loaded
		}
		sn.NodeDetails = details
		speeds := make(map[string]DownloadTest, len(d.NodeSpeeds))
		for _, view := range views {
			stored, saved := d.NodeSpeeds[view.ID]
			previous, inSession := sn.NodeSpeeds[view.ID]
			if !saved && !inSession {
				continue
			}
			loaded := DownloadTest{DownloadSpeed: stored, revision: view.revision}
			if previous.revision.Equal(view.revision) {
				loaded.Running, loaded.Error, loaded.request = previous.Running, previous.Error, previous.request
				loaded.CurrentBytes, loaded.CurrentBytesPerSecond = previous.CurrentBytes, previous.CurrentBytesPerSecond
			}
			speeds[view.ID] = loaded
		}
		sn.NodeSpeeds = speeds
		for i := range sources {
			for _, previous := range sn.Sources {
				if previous.ID == sources[i].ID {
					sources[i].Updating = previous.Updating
					break
				}
			}
		}
		sn.Sources = sources
		sn.Rules = ruleViews(d.Rules)
		sn.Settings = settingsViewOf(d.Settings)
		sn.ConsentRequired = consentRequired
		sn.Mode = d.Settings.Mode
		if sn.Phase != PhaseConnected || sn.Active == "" {
			sn.Active = d.Settings.ActiveProfile
		}
		sn.DataRoot = s.paths.Root
		sn.DataRootDefault = s.paths.DefaultRoot
		sn.DataRootEditable = s.paths.Editable()
		sn.DataRootNotice = dataRootNotice(s.paths)
		sn.GeoReady = s.paths.HasGeoData()
	})
	if !s.noResolve && !consentRequired {
		s.resolveProfiles(views)
	}
	s.configureLogLevel(d.Settings.LogLevel)
	return nil
}

// Toggle 空闲时连接、否则断开：首页只有一个按钮。
func (s *Service) Toggle() {
	if s.state.Snapshot().Phase == PhaseConnected {
		s.Disconnect()
		return
	}
	s.Connect("")
}

// AttachGeo 给服务注入 geo 下载器并发布当前下载状态。启动时调用一次；
// 测试可跳过。
func (s *Service) AttachGeo(d *geo.Downloader) {
	s.mu.Lock()
	s.geo = d
	s.mu.Unlock()
	if d == nil {
		return
	}
	d.SetProgress(s.onGeoStatus)
	s.onGeoStatus(d.Status())
}

// AttachStats 给服务注入计数源，连接后即可启动流量采样。启动时调用一次；
// 测试可跳过。
func (s *Service) AttachStats(src StatsSource) {
	s.mu.Lock()
	s.stopSamplingLocked()
	s.closeStatsLocked()
	s.stats = src
	s.mu.Unlock()
}

// startSamplingLocked 为新连接启动流量采样。调用方持有 mu。
func (s *Service) startSamplingLocked() {
	if s.stats == nil {
		return
	}
	s.stopSamplingLocked()

	// 只读取代理出站的计数：那才是承载用户流量的隧道。累加所有出站会把
	// API 自身的回环字节也算作用户流量。
	names := []string{
		configgen.Name(configgen.ScopeOutbound, configgen.TagProxy, configgen.DirUplink),
		configgen.Name(configgen.ScopeOutbound, configgen.TagProxy, configgen.DirDownlink),
	}
	source := s.stats
	id := s.state.Snapshot().Active
	if len(s.policyCounterNodes) > 0 {
		names = []string{}
		for tag := range s.policyCounterNodes {
			names = append(
				names,
				configgen.Name(configgen.ScopeOutbound, tag, configgen.DirUplink),
				configgen.Name(configgen.ScopeOutbound, tag, configgen.DirDownlink),
			)
		}
		source = &policyCounterSource{StatsSource: s.stats, service: s, nodes: s.policyCounterNodes, last: map[string]int64{}}
		id = ""
	}
	previous := store.Transfer{}
	s.sampler = NewSampler(source, names, func(t Traffic) {
		s.recordTraffic(id, &previous, t)
	})
	s.sampler.Start(s.sampleInterval())
}

func (s *Service) sampleInterval() time.Duration {
	if s.windowHidden.Load() {
		return SampleHidden
	}
	return SampleVisible
}

// SetWindowVisible 降低后台轮询频率。原生回调立即发布最新可见性；工作
// goroutine 不会因连接操作阻塞窗口，即使事件合并也会应用最新值。
func (s *Service) SetWindowVisible(visible bool) {
	if s.windowHidden.Swap(!visible) == !visible {
		return
	}
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.sampler != nil {
			s.sampler.SetInterval(s.sampleInterval())
		}
	}()
}

// stopSamplingLocked 结束采样。调用方持有 mu。
func (s *Service) stopSamplingLocked() {
	if s.sampler != nil {
		s.sampler.Stop()
		s.sampler = nil
		if err := s.flushTrafficLocked(); err != nil {
			slog.Error("写入流量统计失败", "错误", err)
		}
	}
}

func (s *Service) closeStatsLocked() {
	if s.statsClient != nil {
		_ = s.statsClient.Close()
		if s.stats == s.statsClient {
			s.stats = nil
		}
		s.statsClient = nil
	}
}

// EnsureGeo 在文件缺失时启动后台下载。它立即返回：文件到达前应用仍可用。
func (s *Service) EnsureGeo() {
	s.mu.Lock()
	d := s.geo
	s.mu.Unlock()
	if d == nil {
		return
	}
	if d.Present() {
		s.state.Update(func(sn *Snapshot) {
			sn.GeoReady = true
			sn.GeoState = "ready"
			sn.GeoDone, sn.GeoTotal, sn.GeoError = 0, 0, ""
		})
		return
	}
	d.Start()
}

// RetryGeo 取消卡住或失败的下载并重新开始。
func (s *Service) RetryGeo() {
	s.mu.Lock()
	d := s.geo
	s.mu.Unlock()
	if d == nil {
		return
	}
	d.Cancel()
	s.state.Update(func(sn *Snapshot) { sn.GeoError = "" })
	d.Start()
}

// onGeoStatus 把下载器状态同步到 UI 状态。
func (s *Service) onGeoStatus(st geo.Status) {
	s.state.Update(func(sn *Snapshot) {
		sn.GeoState = st.State
		sn.GeoDone, sn.GeoTotal = st.Done, st.Total
		sn.GeoError = geoMessage(st.Err)
		if st.State == "ready" {
			sn.GeoReady = true
		}
	})
}

// geoMessage 把下载错误转换为用户可据以行动的文字。
func geoMessage(err string) string {
	if err == "" {
		return ""
	}
	return "规则库下载失败，将先使用不带 geoip/geosite 的规则（" + TrimDetail(err) + "）"
}

// Connect 用指定节点（或当前节点）启动内核。
//
// 它立即返回。结果以 PhaseConnected 或 PhaseFailed + Problem 出现在 State 中。
func (s *Service) Connect(profileID string) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		// 双击或重放请求：连接中或已连接时忽略。否则第二次 Start 会被
		// 内核拒绝（已在运行），把正常运行的隧道报告成 CORE_START_FAILED，
		// 阶段翻成 Failed。
		switch s.state.Snapshot().Phase {
		case PhaseConnected, PhaseConnecting:
			return
		}

		s.state.SetPhase(PhaseConnecting, nil)

		if err := s.connectLocked(profileID); err != nil {
			slog.Error("连接节点失败", "错误", err)
		}
	}()
}

// connectLocked 执行实际工作；调用方持有 mu。
func (s *Service) connectLocked(profileID string) error {
	d, err := s.store.Load()
	if err != nil {
		s.fail("STORE_ERROR", "读取配置失败", err)
		return err
	}
	if check, ok := s.engine.(interface{ CheckMode(string) error }); ok {
		if err := check.CheckMode(d.Settings.Mode); err != nil {
			s.fail("TUN_UNAVAILABLE", err.Error(), nil)
			return err
		}
	}

	if profileID == "" {
		profileID = d.Settings.ActiveProfile
	}
	if profileID == "" && len(d.Profiles) > 0 {
		profileID = d.Profiles[0].ID
	}
	if profileID == "" {
		s.fail("NO_PROFILE", "还没有节点", nil)
		return fmt.Errorf("没有可连接的节点")
	}

	var prof *store.Profile
	for i := range d.Profiles {
		if d.Profiles[i].ID == profileID {
			prof = &d.Profiles[i]
			break
		}
	}
	if prof == nil {
		s.fail("NO_PROFILE", "找不到该节点", nil)
		return fmt.Errorf("找不到节点 %s", profileID)
	}

	secret, err := s.secretOf(*prof)
	if err != nil {
		s.fail("SECRET_MISSING", "节点缺少凭据", err)
		return err
	}

	in := s.buildInputWithRules(*prof, secret, d.Settings, d.Rules)
	if d.Settings.ActiveGroup != "" && d.Settings.RoutingMode != "direct" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		err = s.preparePolicyInput(ctx, &in, d)
		cancel()
		if err != nil {
			s.fail("POLICY_INVALID", "策略组无法使用", err)
			return err
		}
	} else if provider, ok := s.engine.(interface {
		PrepareNode(context.Context, configgen.Outbound) (configgen.Outbound, error)
	}); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		in.Active, err = provider.PrepareNode(ctx, in.Active)
		cancel()
		if err != nil {
			s.fail("NODE_TLS_FAILED", "无法读取节点服务器证书", err)
			return err
		}
	}
	cfg, diags, err := configgen.Build(in)
	if err != nil {
		msg := "配置无效"
		if len(diags) > 0 {
			msg = diags[0].Message
		}
		s.fail("CONFIG_INVALID", msg, err)
		return err
	}

	if err := s.engine.Start(s.prepareConfig(cfg)); err != nil {
		s.fail("CORE_START_FAILED", "内核启动失败", err)
		return err
	}
	s.policyCounterNodes = map[string]string{}
	if in.ActivePolicy != "" && in.Settings.RoutingMode != "direct" {
		for _, node := range in.PolicyNodes {
			s.policyCounterNodes[configgen.PolicyNodePrefix+node.ID] = node.ID
		}
	}
	if monitor, ok := s.engine.(interface{ SetConnectionNode(string, string) }); ok {
		monitor.SetConnectionNode(prof.Name, string(prof.Protocol))
	}

	// 只有在系统代理模式下、且内核启动后，才把系统代理指向混合入站：
	// 指向一个无人监听的端口会在变更期间中断浏览器。
	//
	// 这里的失败只报告，不让连接失败：内核已在运行；即使操作系统拒绝
	// 修改代理设置，用户仍可手动把浏览器设为 127.0.0.1:端口。
	if d.Settings.Mode == "system-proxy" {
		if layer, ok := s.systemProxy.(*proxyLayer); ok {
			if err := s.applySystemProxyLocked(layer, "127.0.0.1", d.Settings.MixedPort); err != nil {
				slog.Error("应用系统代理失败", "错误", err)
			}
		}
	}

	s.state.Update(func(sn *Snapshot) {
		sn.Phase = PhaseConnected
		sn.Since = time.Now()
		sn.Active = profileID
		sn.Problem = nil
		sn.Traffic = Traffic{}
		sn.History = nil
	})

	// API 入站此时已监听，可以读取计数。先前挂载的统计源（测试中的假实现）
	// 保留；否则为本次连接创建真实客户端。
	if s.stats == nil && !s.noRealStats {
		s.attachRealStatsLocked()
	}
	s.startSamplingLocked()
	return nil
}

// attachRealStatsLocked 连接真实 API 客户端。调用方持有 mu，且内核已启动，
// 因此端口正在监听。
func (s *Service) attachRealStatsLocked() {
	port := s.apiPortLocked()
	if port <= 0 {
		return
	}
	client := apiclient.New(port)
	if err := client.Connect(context.Background()); err != nil {
		slog.Error("连接统计 API 失败", "端口", port, "错误", err)
		return
	}
	s.stats = client
	s.statsClient = client
}

// ImportText 解析分享链接或订阅内容并保存。
//
// 它立即返回；结果出现在 Snapshot.ImportAdded 与 Snapshot.ImportProblem。
func (s *Service) ImportText(text string) {
	s.Import(ImportInput{Text: text})
}

// importText 执行实际工作，测试无需轮询即可调用。
func (s *Service) importText(text string) {
	if text == "" {
		s.setImport(0, "粘贴分享链接或订阅内容后再导入")
		return
	}

	res := suburl.ParseText(text)
	if len(res.Profiles) == 0 {
		msg := "没有可导入的节点"
		if len(res.Diagnostics) > 0 {
			msg = res.Diagnostics[0].Message
		}
		s.setImport(0, msg)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	policyNodes := map[string]string{}
	for i := range res.Profiles {
		p := res.Profiles[i]
		p.ID = newID()
		policyNodes[p.Name] = p.ID
		if err := s.storeProfile(&p); err != nil {
			s.setImport(0, "保存节点失败："+TrimDetail(err.Error()))
			return
		}
	}
	if len(res.Groups) > 0 {
		d, err := s.store.Load()
		if err == nil {
			d.Groups = mergePolicyGroups(d.Groups, res.Groups, "local:"+newID(), policyNodes)
			err = s.store.Save(d)
		}
		if err != nil {
			s.setImport(len(res.Profiles), "保存策略组失败")
			return
		}
	}
	if err := s.Load(); err != nil {
		s.setImport(0, "读取配置失败："+TrimDetail(err.Error()))
		return
	}
	s.setImport(len(res.Profiles), "")
}

// setImport 记录最近一次导入的结果。
func (s *Service) setImport(added int, problem string) {
	s.state.Update(func(sn *Snapshot) {
		sn.ImportAdded = added
		sn.ImportProblem = problem
	})
}

// Disconnect 停止内核并清空计数。
func (s *Service) Disconnect() {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		_ = s.disconnectLocked()
	}()
}

// disconnectLocked 在停止内嵌内核前还原系统设置。调用方持有 mu，
// 包括因路由变更而重启时。
func (s *Service) disconnectLocked() error {
	// 空闲时无事可做：重复的断开请求不应把阶段翻转成 Disconnecting，
	// 也不应再次执行还原。内核运行中时阶段必然不是 Idle，因此这里
	// 不会漏掉真实的断开。
	if s.state.Snapshot().Phase == PhaseIdle {
		return nil
	}

	// 在内核停止前停止采样，否则下一次读取会打到已关闭的端口，
	// UI 会把旧速率当成当前值。
	s.stopSamplingLocked()
	s.closeStatsLocked()

	// 在内核停止之前撤销系统变更（计划 S9：先还原副作用再 Close）。
	// 顺序很重要：还原后的 DNS 与代理在恢复期间仍必须有可指向的对象；
	// 半还原且内核已消失，正是“既没网也没代理”的状态。
	if err := s.effectsLocked().RestoreAll(s.restorer()); err != nil {
		slog.Error("断开时还原系统变更失败", "错误", err)
	}

	s.state.SetPhase(PhaseDisconnecting, nil)
	if err := s.engine.Stop(); err != nil {
		s.fail("CORE_STOP_FAILED", "内核停止失败", err)
		return err
	}
	s.state.Update(func(sn *Snapshot) {
		sn.Phase = PhaseIdle
		sn.Problem = nil
		sn.Traffic = Traffic{}
	})
	return nil
}

// storeProfile 为解析出的节点分配 ID，并连同凭据一起保存。
func (s *Service) storeProfile(p *store.Profile) error {
	p.RawOutbound = withoutOutboundSecret(p.RawOutbound)
	if p.SecretRef != "" {
		key := "profile/" + p.ID + "/secret"
		if err := s.secrets.Set(key, []byte(p.SecretRef)); err != nil {
			return err
		}
		p.SecretRef = key
	}
	return s.store.PutProfile(*p)
}

// secretOf 从凭据保管器读回节点凭据。
func (s *Service) secretOf(p store.Profile) (string, error) {
	if p.SecretRef == "" {
		return "", fmt.Errorf("节点 %s 缺少凭据", p.ID)
	}
	raw, err := s.secrets.Get(p.SecretRef)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// SetActive 记录连接按钮使用的节点。
func (s *Service) SetActive(id string) <-chan struct{} {
	return s.runAsync(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		d, err := s.store.Load()
		if err != nil {
			return
		}
		d.Settings.ActiveProfile = id
		d.Settings.ActiveGroup = ""
		if err := s.store.SetSettings(d.Settings); err != nil {
			slog.Error("保存选中节点失败", "错误", err)
			return
		}
		s.state.Update(func(sn *Snapshot) { sn.Active = id; sn.Settings.ActiveGroup = "" })
	})
}

// SetMode 在系统代理与 TUN 之间切换，必要时重连。
func (s *Service) SetMode(mode string) {
	go func() {
		if s.saveMode(mode) {
			s.Disconnect()
			waitFor(s.state, PhaseIdle, 5*time.Second)
			s.Connect("")
		}
	}()
}

// saveMode 与设置保存、节点变更共用读-改-写锁。先返回再重连，
// 因为重连需要同一把操作锁。
func (s *Service) saveMode(mode string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch mode {
	case "system-proxy", "tun", "direct":
	default:
		s.setProblem("SETTINGS_INVALID", "不支持的代理模式", nil)
		return false
	}
	d, err := s.store.Load()
	if err != nil {
		s.setProblem("STORE_ERROR", "读取配置失败", err)
		return false
	}
	d.Settings.Mode = mode
	if err := s.store.SetSettings(d.Settings); err != nil {
		s.setProblem("STORE_ERROR", "保存模式失败", err)
		return false
	}
	s.state.Update(func(sn *Snapshot) {
		sn.Mode = mode
		sn.Settings.Mode = mode
	})
	return s.state.Snapshot().Phase == PhaseConnected
}

// prepareConfig 设置 Xray 读取数据文件所需的环境。
//
// Xray 通过 `xray.location.asset` 环境变量查找 geoip.dat 与 geosite.dat，
// 因此必须在实例启动前指向数据目录。文件缺失时 geo 规则
// 只是加载失败，所以配置生成器只在文件存在时才输出这些规则。
func (s *Service) prepareConfig(cfg []byte) []byte {
	_ = os.Setenv(assetLocationEnv, s.paths.Root)
	return cfg
}

// assetLocationEnv 是 Xray 用于指定数据文件目录的环境变量。
const assetLocationEnv = "xray.location.asset"

// buildInputWithRules 组装当前节点、设置与路由规则。
//
// 规则只在这里进入生成器，因此屏幕上的规则就是配置里的规则，反之亦然。
func (s *Service) buildInputWithRules(
	active store.Profile,
	secret string,
	set store.Settings,
	rules []store.Rule,
) configgen.Input {
	in := configgen.Input{
		Active: outboundOf(active, secret),
		Mode:   configgen.Mode(set.Mode),
		Rules:  configRules(rules),
		Settings: configgen.Settings{
			Mode:                 configgen.Mode(set.Mode),
			TunName:              defaultTunName(),
			TunGateway:           []string{"172.19.0.1/30", "fc00::1/126"},
			TunOutboundInterface: "auto",
			RoutingMode:          set.RoutingMode,
			MixedPort:            set.MixedPort,
			LogLevel:             set.LogLevel,
			ProbeURL:             set.ProbeURL,
			APIPort:              s.apiPortLocked(),
			// 只有 geo 数据文件存在时才生成 geo 规则；否则构建会报告
			// 警告并使用字面范围。
			GeoReady: s.paths.HasGeoData(),
		},
	}
	// DNS 上游只接受 IP 形式：主机名上游可能经由它自己供给的隧道解析
	//（计划 P11）。
	in.Settings.DNSServers = defaultDNS()
	// 两个本地辅助入站都需要空闲端口；5353 和固定 API 端口在实践中冲突
	// 频繁，足以导致启动失败。
	in.Settings.DNSPort = s.dnsPortLocked()
	in.Settings.APIPort = s.apiPortLocked()
	return in
}

func defaultTunName() string { return "utun7" }

// dnsPortLocked 为本地 DNS 入站返回一个空闲端口。
func (s *Service) dnsPortLocked() int {
	if s.dnsPort != 0 {
		return s.dnsPort
	}
	if p, err := freePortNumber(); err == nil {
		s.dnsPort = p
	}
	return s.dnsPort
}

// defaultDNS 是用户选择之前使用的上游列表。
func defaultDNS() []string {
	return []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"}
}

// fail 在状态中记录一个问题。
func (s *Service) fail(code, message string, cause error) {
	p := &Problem{Code: code, Message: message}
	if cause != nil {
		p.Detail = cause.Error()
	}
	s.state.SetPhase(PhaseFailed, p)
}

// waitFor 阻塞到状态到达指定阶段或超时。
func waitFor(st *State, phase Phase, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st.Snapshot().Phase == phase {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// newID 返回 128 位随机标识的十六进制字符串。
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("id-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// SetAPIPort 固定 API 入站端口。应用启动时设置空闲端口；测试可设置自己的端口。
func (s *Service) SetAPIPort(port int) {
	s.mu.Lock()
	s.apiPort = port
	s.mu.Unlock()
}

// apiPortLocked 返回 API 端口，首次调用时选一个空闲端口。
func (s *Service) apiPortLocked() int {
	if s.apiPort != 0 {
		return s.apiPort
	}
	if p, err := freePortNumber(); err == nil {
		s.apiPort = p
	}
	return s.apiPort
}

// freePortNumber 向操作系统申请一个未使用的 TCP 端口。
func freePortNumber() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// TrimDetail 把错误文本压缩成 UI 可用的一行短文本。
func TrimDetail(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

var _ Engine = (*xcore.Engine)(nil)

// ProfileEdit 是节点可编辑的部分，由编辑对话框收集。
type ProfileEdit struct {
	ID       string
	Name     string
	Address  string
	Port     int
	Favorite bool
}

// EditProblem 是一个无法保存的字段，以用户需要修复的控件为键。对话框把
// 消息渲染在该控件下方并禁用保存，从而在进入存储前拦截坏节点。
type EditProblem struct {
	Field   string // 字段：name | address | port | publicKey
	Message string
}

// EditProblems 返回编辑内容相对已存储节点的全部问题。空结果表示可以保存。
//
// 公钥不可编辑：它来自分享链接并保存在出站配置中。缺少公钥的 REALITY
// 节点根本无法连接，因此对话框没有有用的补救办法；诚实的做法是拒绝
// 保存并说明原因，而不是存下一个稍后以 TLS 错误失败的节点。
func EditProblems(e ProfileEdit, stored store.Profile) []EditProblem {
	var out []EditProblem
	if strings.TrimSpace(e.Name) == "" {
		out = append(out, EditProblem{"name", "名称不能为空"})
	}
	if strings.TrimSpace(e.Address) == "" {
		out = append(out, EditProblem{"address", "地址不能为空"})
	}
	if e.Port <= 0 || e.Port > 65535 {
		out = append(out, EditProblem{"port", "端口需在 1–65535 之间"})
	}
	if strings.EqualFold(string(stored.Protocol), "vless") {
		var o configgen.Outbound
		if len(stored.RawOutbound) > 0 {
			_ = json.Unmarshal(stored.RawOutbound, &o)
		}
		if o.Security == "reality" && strings.TrimSpace(o.PublicKey) == "" {
			out = append(out, EditProblem{"publicKey", "REALITY 节点缺少公钥（pbk），无法连接"})
		}
	}
	return out
}

// Prober 返回延迟探测器，首次使用时创建。
//
// 视图通过它读取结果，自己从不拨号，因为一帧绝不能阻塞。
// 重绘通过与状态相同的回调请求，因此结果落地后会在下一帧出现。
func (s *Service) Prober() *Prober {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	if s.prober == nil {
		s.prober = NewProber()
		s.prober.SetOnChange(s.state.notifier())
	}
	return s.prober
}

// proberForSettings 把用户配置应用到探测器。设置在每次启动测试时读取，
// 因此保存后无需重启应用，也不会影响正在进行的上一轮测试。
func (s *Service) proberForSettings(set store.Settings) *Prober {
	timeoutMS := set.LatencyTimeoutMS
	if timeoutMS < LatencyTimeoutMSMin || timeoutMS > LatencyTimeoutMSMax {
		timeoutMS = store.DefaultSettings().LatencyTimeoutMS
	}
	prober := s.Prober()
	prober.SetLimits(set.LatencyConcurrency, time.Duration(timeoutMS)*time.Millisecond)
	return prober
}

// TestAll 探测所有已存储节点。
//
// 列表在返回前读取，因此紧接着等待本次运行的调用方（或测试）不会与
// 存储读取竞争而找不到运行记录。存储读取很廉价且已被上次 Load 缓存；
// 真正必须离开调用线程的是探测本身。
func (s *Service) TestAll() {
	d, err := s.store.Load()
	if err != nil {
		s.setProblem("STORE_ERROR", "读取配置失败", err)
		return
	}
	s.proberForSettings(d.Settings).Start(d.Profiles)
}

// CancelTest 停止测速运行。
func (s *Service) CancelTest() {
	s.viewMu.RLock()
	p := s.prober
	s.viewMu.RUnlock()
	if p != nil {
		p.Cancel()
	}
}

// Latencies 返回每个节点的探测结果，供列表绘制。
func (s *Service) Latencies() map[string]Latency {
	s.viewMu.RLock()
	p := s.prober
	s.viewMu.RUnlock()
	if p == nil {
		return nil
	}
	return p.Results()
}

// Testing 返回是否有测速正在运行。
func (s *Service) Testing() bool {
	s.viewMu.RLock()
	p := s.prober
	s.viewMu.RUnlock()
	return p != nil && p.Running()
}

// Profile 按 ID 返回已存储节点，供需要列表未包含字段的视图使用
// （编辑对话框读取 RawOutbound 以校验节点）。
//
// 这是存储读取，只适合点击时调用，绝不能用于每帧绘制：列表本身从
// Snapshot 取数。
func (s *Service) Profile(id string) (store.Profile, bool) {
	p, err := s.store.GetProfile(id)
	if err != nil {
		return store.Profile{}, false
	}
	return p, true
}

// SaveProfile 把节点可编辑字段写回存储。
//
// 只改动这些字段：节点的协议、凭据和出站细节来自分享链接或订阅；
// 用手工表单重写它们，正是把可用节点变成坏节点的方式。
func (s *Service) SaveProfile(e ProfileEdit) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		d, err := s.store.Load()
		if err != nil {
			s.setProblem("STORE_ERROR", "读取配置失败", err)
			return
		}
		idx := -1
		for i := range d.Profiles {
			if d.Profiles[i].ID == e.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			s.setProblem("NO_PROFILE", "找不到该节点", nil)
			return
		}

		p := d.Profiles[idx]
		p.Name = e.Name
		p.Address = e.Address
		p.Port = e.Port
		p.Favorite = e.Favorite
		p.UpdatedAt = time.Now()
		p.RawOutbound = withoutOutboundSecret(p.RawOutbound)
		if err := s.store.PutProfile(p); err != nil {
			s.setProblem("STORE_ERROR", "保存节点失败", err)
			return
		}
		if err := s.Load(); err != nil {
			s.setProblem("STORE_ERROR", "读取配置失败", err)
		}
	}()
}

// DeleteProfiles 删除节点及其凭据。
func (s *Service) DeleteProfiles(ids []string) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		snap := s.state.Snapshot()
		for _, id := range ids {
			if id == snap.Active && snap.Phase == PhaseConnected {
				s.setProblem("NODE_IN_USE", "请先断开当前节点再删除", nil)
				return
			}
		}

		d, err := s.store.Load()
		if err != nil {
			s.setProblem("STORE_ERROR", "读取配置失败", err)
			return
		}
		drop := make(map[string]bool, len(ids))
		for _, id := range ids {
			drop[id] = true
		}
		kept := make([]store.Profile, 0, len(d.Profiles))
		refs := []string{}
		for _, profile := range d.Profiles {
			if drop[profile.ID] {
				refs = append(refs, profile.SecretRef)
			} else {
				kept = append(kept, profile)
			}
		}
		d.Profiles = kept
		if drop[d.Settings.ActiveProfile] {
			d.Settings.ActiveProfile = ""
		}
		// 先提交列表与选中项，再移除凭据。
		if err := s.store.Save(d); err != nil {
			s.setProblem("STORE_ERROR", "删除节点失败", err)
			return
		}
		for _, ref := range refs {
			if ref != "" {
				_ = s.secrets.Delete(ref)
			}
		}
		if err := s.Load(); err != nil {
			s.setProblem("STORE_ERROR", "读取配置失败", err)
		}
	}()
}

// setProblem 记录问题但不改变连接阶段：这些操作发生时内核可能正在运行，
// 切到 Failed 会错误地暗示隧道已断开。
func (s *Service) setProblem(code, message string, cause error) {
	p := &Problem{Code: code, Message: message}
	if cause != nil {
		p.Detail = TrimDetail(cause.Error())
	}
	s.state.Update(func(sn *Snapshot) { sn.Problem = p })
}

// AttachLogs 给服务注入日志页读取的日志缓冲。
func (s *Service) AttachLogs(l LogSource) {
	s.viewMu.Lock()
	s.logs = l
	s.viewMu.Unlock()
	s.configureLogLevel(s.state.Snapshot().Settings.LogLevel)
}

// Xray 启动时会替换自己的日志器；因此收集器持有采集阈值，
// 包括内核运行期间的修改。
func (s *Service) configureLogLevel(value string) {
	if value == "" {
		value = "warning"
	}
	s.viewMu.RLock()
	defer s.viewMu.RUnlock()
	if configurable, ok := s.logs.(interface{ SetLevel(logs.Level) }); ok {
		configurable.SetLevel(logs.ParseLevel(value))
	}
}

// Logs 返回内存中的日志行，最旧的在前。它从不在内核上阻塞：
// 缓冲由独立 goroutine 填充。
func (s *Service) Logs() []logs.Entry {
	s.viewMu.RLock()
	l := s.logs
	s.viewMu.RUnlock()
	if l == nil {
		return nil
	}
	return l.Snapshot()
}

// ClearLogs 清空内存中的日志行。磁盘文件保留：它才是事件记录。
func (s *Service) ClearLogs() {
	s.viewMu.RLock()
	l := s.logs
	s.viewMu.RUnlock()
	if l != nil {
		l.Clear()
	}
}

// DroppedLogs 返回因写入器落后而丢失的行数，让日志页能说明视图不完整，
// 而不是假装完整。
func (s *Service) DroppedLogs() int64 {
	s.viewMu.RLock()
	l := s.logs
	s.viewMu.RUnlock()
	if l == nil {
		return 0
	}
	return l.Dropped()
}
