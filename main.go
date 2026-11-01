// vRay 是一个原生 UI（MyGO + CrychicUI）的 Xray 客户端，内核以内嵌库方式运行于同进程。
package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ELY-Open-Source/CrychicUI/sound"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/core/logs"
	"github.com/sbgayhub/vray/internal/geo"
	"github.com/sbgayhub/vray/internal/logging"
	"github.com/sbgayhub/vray/internal/paths"
	"github.com/sbgayhub/vray/internal/secrets"
	"github.com/sbgayhub/vray/internal/shell"
	"github.com/sbgayhub/vray/internal/store"
	"github.com/sbgayhub/vray/internal/sysproxy"
	vui "github.com/sbgayhub/vray/internal/ui"
	"github.com/sbgayhub/vray/resources"

	// 应用自身消息的生成目录。导入它们即声明应用支持的语言，
	// 这既是 `mygo build` 的要求，也是翻译串能在运行时被找到的前提。
	_ "github.com/sbgayhub/vray/locales"
)

func main() {
	// 控制台日志：中文级别名 + 键值属性，必须在任何可能出错的步骤之前就绪。
	slog.SetDefault(slog.New(logging.NewConsoleHandler(os.Stderr, logging.ConsoleOptions{
		Level: slog.LevelInfo,
	})))

	sound.SetEnabled(false)
	// 本地化目录由 locales 包内嵌并注册。

	// 默认使用当前用户的标准数据目录；便携模式、VRAY_DATA 覆盖和旧目录
	// 迁移都由 paths 包统一处理。
	p, err := paths.Resolve()
	if err != nil {
		fatal("解析数据目录失败", err)
	}
	if p.Migrated {
		slog.Info("已迁移数据目录", "来源", p.MigrationSource, "目标", p.Root)
	}
	if p.MigrationBackup != "" {
		slog.Info("已备份默认目录中的旧数据", "备份", p.MigrationBackup)
	}
	if p.Warning != "" {
		slog.Warn("数据目录需要处理", "说明", p.Warning)
	}
	if err := p.Prepare(); err != nil {
		fatal("初始化数据目录失败", err)
	}

	st := app.NewState(app.Snapshot{
		Phase:            app.PhaseIdle,
		Mode:             store.DefaultSettings().Mode,
		DataRoot:         p.Root,
		DataRootDefault:  p.DefaultRoot,
		DataRootEditable: p.Editable(),
		GeoReady:         p.HasGeoData(),
	})

	// 一个内核实例，加一个由它供给日志的收集器。收集器持有
	// <数据目录>/logs/vray.log 与日志页读取的环形缓冲。
	engine := xcore.New()
	logPath := filepath.Join(p.Logs(), "vray.log")
	collector, err := logs.NewCollector(logs.DefaultRing, logPath)
	if err != nil {
		// 日志文件打不开也不能拦住应用启动：退回仅内存模式，
		// 本次会话的日志页仍然可用。
		slog.Warn("日志文件不可用，本次会话仅保留内存日志", "错误", err)
		collector, err = logs.NewCollector(logs.DefaultRing, "")
		if err != nil {
			fatal("初始化日志收集器失败", err)
		}
	}
	defer collector.Close()
	engine.SetLogger(collector)

	svc := app.New(
		st,
		store.NewFile(p.DB()),
		secrets.NewSystem("dev.sbgayhub.vray", p.Secrets()),
		engine,
		p,
	)
	svc.AttachLogs(collector)
	// 平台效果层：恢复系统变更经由这些层执行，ApplySystemProxy
	// 也通过同一个对象写入代理设置。
	proxyBackend := sysproxy.New()
	svc.AttachEffectLayers(app.NewProxyLayer(proxyBackend), nil)
	svc.AttachLoginSetter(shell.SetOpenAtLogin)
	if err := svc.Load(); err != nil {
		fatal("加载应用数据失败", err)
	}

	// 崩溃补偿的第 3 层：若上次运行留下了系统变更，
	// 在其他事情发生前先撤销。第 2 层是下方的退出钩子；第 1 层是
	// Disconnect 自带的还原。
	if recovered, err := svc.RecoverEffects(); err != nil {
		// 只报告，不终止：代理仍可工作；若拒绝启动，用户反而无法
		// 进入设置去修复它。
		slog.Error("恢复上次会话的系统变更失败", "错误", err)
	} else if len(recovered) > 0 {
		slog.Info("已撤销上次运行遗留的系统变更", "数量", len(recovered))
	}

	// geo 数据文件就位前 Xray 无法按 geoip/geosite 分流，因此在后台
	// 下载，应用保持可用。
	geoDL := geo.New(p.Root)
	svc.AttachGeo(geoDL)
	defer geoDL.Cancel()

	root := vui.NewRoot(svc)
	background, cancelBackground := context.WithCancel(context.Background())
	defer cancelBackground()
	svc.AttachUpdateBackend(app.NewMyGOUpdateBackend())
	svc.SetPostConsentStart(func() {
		// 首次 Load 发生在同意之前，以便先展示设置与本地数据；现在
		// 重新加载，才会启动 DNS 解析与各网络后台任务。
		if err := svc.Load(); err != nil {
			slog.Error("同意后重新加载数据失败", "错误", err)
		}
		svc.EnsureGeo()
		svc.RunSourceScheduler(background)
		svc.StartUpdateScheduler(background)
	})
	if !svc.State().Snapshot().ConsentRequired {
		svc.StartPostConsent()
	}

	mygo.App.WhenReady(func() {
		mygo.Theme.SetSource(mygo.ThemeSource(st.Snapshot().Settings.Theme))
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:             "vRay",
			Width:             1120,
			Height:            760,
			MinWidth:          760,
			MinHeight:         520,
			TitleBarStyle:     mygo.TitleBarHidden,
			TitleBarHeight:    36,
			HideWindowButtons: true,
			Vibrancy:          mygo.VibrancyMica,
			// 在首个任务栏按钮创建之前设置窗口图标。
			Hidden:  true,
			Content: ui.View(root.View),
		})
		if err := win.SetIcon(resources.Icon); err != nil {
			slog.Error("设置窗口图标失败", "错误", err)
		}
		root.Attach(win)
		life := shell.NewLifecycle(win)
		startedHidden := life.StartedHidden()
		svc.SetWindowVisible(!startedHidden)
		win.OnShow(func() { svc.SetWindowVisible(true) })
		win.OnRestore(func() { svc.SetWindowVisible(true) })
		win.OnHide(func() { svc.SetWindowVisible(false) })
		win.OnMinimize(func() { svc.SetWindowVisible(false) })
		mygo.Theme.OnUpdated(win.Invalidate)

		life.CloseHides(st.Snapshot().Settings.CloseToTray)
		// 第 2 层：托盘或系统发起的退出在进程结束前还原系统变更，
		// 正常退出永远不会留下未清空的效果日志。
		life.OnQuit(func() {
			cancelBackground()
			svc.CancelInspection()
			if err := svc.FlushTraffic(); err != nil {
				slog.Error("退出时写入流量统计失败", "错误", err)
			}
			if err := svc.RestoreEffects(); err != nil {
				slog.Error("退出时还原系统变更失败", "错误", err)
			}
		})

		tray, err := shell.NewTray(win, svc, life.QuittingPtr())
		if err != nil {
			// 没有托盘图标，隐藏的窗口将无处可回，因此关闭按钮
			// 必须真正关闭窗口。
			slog.Error("创建托盘图标失败", "错误", err)
			life.CloseHides(false)
		} else {
			tray.Refresh(st.Snapshot())
			// 绝不能 `defer tray.Destroy()`：本回调在完成初始化后立即返回，
			// 延迟销毁会让图标刚创建就被拆掉——那正是“没有托盘图标”
			// 故障的外在表现。图标与进程同寿命；真正退出时由托盘
			// 自己的“退出”菜单项触发 Destroy。
		}

		// 状态变更请求重绘，视图从不轮询；托盘跟随同一信号，
		// 两者显示的阶段始终一致。
		updates := make(chan struct{}, 1)
		stopped := make(chan struct{})
		mygo.App.OnQuit(func() { close(stopped) })
		st.SetOnChange(func() {
			win.Invalidate()
			select {
			case updates <- struct{}{}:
			default:
			}
		})
		// 原生外壳更新必须在主线程执行，即使隐藏的窗口已暂停帧。
		// 合并采样信号，不阻塞工作 goroutine。
		go func() {
			for {
				select {
				case <-stopped:
					return
				case <-updates:
					mygo.RunOnMain(func() {
						snap := st.Snapshot()
						life.CloseHides(tray != nil && snap.Settings.CloseToTray)
						if tray != nil {
							tray.Refresh(snap)
						}
					})
				}
			}
		}()
		// 登录时启动的实例保持隐藏，直到托盘打开窗口。
		if !startedHidden {
			win.Show()
		}
	})

	if err := mygo.App.Run(); err != nil {
		fatal("应用主循环异常退出", err)
	}
}

// fatal 记录一条错误日志并以状态码 1 退出进程。
func fatal(msg string, err error) {
	slog.Error(msg, "错误", err)
	os.Exit(1)
}
