package app

import (
	"fmt"
	"net"
	"net/url"
	"runtime"
	"strconv"

	"github.com/sbgayhub/vray/internal/store"
)

// SettingsView 是设置页的模型。
//
// 每次 Load 都从存储构建，页面自己从不读文件：视图不做 IO
// （docs/02 §5.9）。
type SettingsView struct {
	ActiveGroup         string `json:"activeGroup,omitempty"`
	Language            string `json:"language"`
	LookupKeyConfigured bool   `json:"ipLookupConfigured,omitempty"`
	Accent              string `json:"accent"`
	Theme               string `json:"theme"`
	Material            string `json:"material"`
	Mode                string `json:"mode"`
	RoutingMode         string `json:"routingMode"`
	MixedPort           int    `json:"mixedPort"`
	LogLevel            string `json:"logLevel"`
	ProbeURL            string `json:"probeUrl"`
	CloseToTray         bool   `json:"closeToTray"`
	OpenAtLogin         bool   `json:"openAtLogin"`
	AutoReconnect       bool   `json:"autoReconnect"`
	AutoUpdateChecks    bool   `json:"autoUpdateChecks"`
}

// CheckState 表示单项环境检查的结果。
type CheckState string

// 检查结果。warn 表示应用没有它也能工作，页面只提示、不阻塞任何操作。
const (
	CheckOK   CheckState = "ok"
	CheckWarn CheckState = "warn"
	CheckFail CheckState = "fail"
)

// Check 是环境自检中的一行。
type Check struct {
	Name   string     `json:"name"`
	State  CheckState `json:"state"`
	Detail string     `json:"detail,omitempty"`
}

// settingsViewOf 把已存储设置转换为页面模型。
func settingsViewOf(s store.Settings) SettingsView {
	return SettingsView{
		ActiveGroup:         s.ActiveGroup,
		Language:            s.Language,
		LookupKeyConfigured: s.LookupKeyConfigured,
		Accent:              s.Accent,
		Theme:               s.Theme,
		Material:            s.Material,
		Mode:                s.Mode,
		RoutingMode:         s.RoutingMode,
		MixedPort:           s.MixedPort,
		LogLevel:            s.LogLevel,
		ProbeURL:            s.ProbeURL,
		CloseToTray:         s.CloseToTray,
		OpenAtLogin:         s.OpenAtLogin,
		AutoReconnect:       s.AutoReconnect,
		AutoUpdateChecks:    s.AutoUpdateChecks,
	}
}

// SaveSettings 写入可编辑设置并重新加载。
//
// 越界端口会以字段错误拒绝，而不是截断：悄悄改掉用户输入，正是代理
// 在意外位置监听的原因。
func (s *Service) SaveSettings(v SettingsView) <-chan struct{} {
	done := make(chan struct{})
	s.state.Update(func(sn *Snapshot) {
		sn.SettingsSaving = true
		sn.SettingsError = ""
	})
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		defer func() {
			s.state.Update(func(sn *Snapshot) { sn.SettingsSaving = false })
			close(done)
		}()
		fail := func(code, message string, cause error) {
			s.setProblem(code, message, cause)
			s.state.Update(func(sn *Snapshot) { sn.SettingsError = message })
		}

		d, err := s.store.Load()
		if err != nil {
			fail("STORE_ERROR", "读取配置失败", err)
			return
		}
		if v.MixedPort <= 0 || v.MixedPort > 65535 {
			fail("SETTINGS_INVALID", "本地端口需在 1–65535 之间", nil)
			return
		}
		switch v.Mode {
		case "system-proxy", "tun", "direct":
		default:
			fail("SETTINGS_INVALID", fmt.Sprintf("不支持的模式 %q", v.Mode), nil)
			return
		}
		switch v.LogLevel {
		case "debug", "info", "warning", "error", "none":
		default:
			fail("SETTINGS_INVALID", fmt.Sprintf("不支持的日志级别 %q", v.LogLevel), nil)
			return
		}
		if v.ProbeURL != "" && !looksLikeURL(v.ProbeURL) {
			fail("SETTINGS_INVALID", "探测地址必须是 http(s) 链接", nil)
			return
		}
		if v.Theme == "" {
			v.Theme = d.Settings.Theme
			if v.Theme == "" {
				v.Theme = "system"
			}
		}
		if v.Material == "" {
			v.Material = d.Settings.Material
			if v.Material == "" {
				v.Material = "none"
			}
		}
		if v.Accent == "" {
			v.Accent = d.Settings.Accent
		}
		switch v.Theme {
		case "system", "light", "dark":
		default:
			fail("SETTINGS_INVALID", "不支持的主题", nil)
			return
		}
		switch v.Material {
		case "none", "mica", "micaAlt", "acrylic", "liquidGlass":
		default:
			fail("SETTINGS_INVALID", "不支持的窗口材质", nil)
			return
		}

		next := d.Settings
		next.Accent = v.Accent
		next.Theme = v.Theme
		next.Material = v.Material
		next.Mode = v.Mode
		next.MixedPort = v.MixedPort
		next.LogLevel = v.LogLevel
		next.ProbeURL = v.ProbeURL
		next.CloseToTray = v.CloseToTray
		next.OpenAtLogin = v.OpenAtLogin
		next.AutoReconnect = v.AutoReconnect
		next.AutoUpdateChecks = v.AutoUpdateChecks
		changedLogin := next.OpenAtLogin != d.Settings.OpenAtLogin && s.loginSetter != nil
		if changedLogin {
			if err := s.loginSetter(next.OpenAtLogin); err != nil {
				fail("SETTINGS_ERROR", "无法更改开机启动设置", err)
				return
			}
		}

		if err := s.store.SetSettings(next); err != nil {
			if changedLogin {
				if restoreErr := s.loginSetter(d.Settings.OpenAtLogin); restoreErr != nil {
					fail("SETTINGS_ERROR", "保存失败，开机启动设置还原失败", restoreErr)
					return
				}
			}
			fail("STORE_ERROR", "保存设置失败", err)
			return
		}
		if err := s.Load(); err != nil {
			fail("STORE_ERROR", "读取配置失败", err)
		}
	}()
	return done
}

// looksLikeURL 只接受探测可用的两种协议。
func looksLikeURL(s string) bool {
	u, err := url.ParseRequestURI(s)
	if err != nil || u.Hostname() == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// SelfCheck 检查应用当前所处的环境。
//
// 每一行都说明检查对象以及是否正常。只检查真正影响行为的事项：
// geo 数据缺失会让 geo 规则不可用，端口被占用会导致连接失败，
// 平台不支持会改变可用模式。
func (s *Service) SelfCheck() []Check {
	var out []Check

	out = append(out, Check{
		Name:  "运行平台",
		State: CheckOK,
		Detail: fmt.Sprintf("%s/%s · Go %s",
			runtime.GOOS, runtime.GOARCH, runtime.Version()),
	})

	// geo 数据：缺失时规则回退为字面范围。
	if s.paths.HasGeoData() {
		out = append(out, Check{
			Name: "规则数据（geoip/geosite）", State: CheckOK,
			Detail: "已就绪，可使用 geoip:/geosite: 规则",
		})
	} else {
		out = append(out, Check{
			Name:   "规则数据（geoip/geosite）",
			State:  CheckWarn,
			Detail: "未下载：geoip:/geosite: 规则暂不可用，私网仍按内置网段直连",
		})
	}

	// 混合入站需要的端口。
	set, err := s.store.Load()
	if err != nil {
		out = append(out, Check{Name: "配置文件", State: CheckFail, Detail: err.Error()})
	} else {
		out = append(out, Check{Name: "配置文件", State: CheckOK, Detail: "可读写"})
		port := set.Settings.MixedPort
		if portInUse(port) {
			out = append(out, Check{
				Name:   fmt.Sprintf("本地端口 %d", port),
				State:  CheckWarn,
				Detail: "已被占用：连接时可能失败，可在上面换一个",
			})
		} else {
			out = append(out, Check{
				Name:   fmt.Sprintf("本地端口 %d", port),
				State:  CheckOK,
				Detail: "空闲",
			})
		}
	}

	// TUN 需要普通用户没有的权限；在用户选择该模式前说明，
	// 好过连接时才失败。
	out = append(out, tunCheck())

	if runtime.GOOS == "linux" {
		out = append(out, Check{
			Name: "系统代理", State: CheckWarn,
			Detail: "Linux 下系统代理需要桌面环境支持，未自动设置",
		})
	} else {
		out = append(out, Check{Name: "系统代理", State: CheckOK, Detail: "可用"})
	}

	out = append(out, Check{Name: "数据目录", State: CheckOK, Detail: s.paths.Root})
	return out
}

// RunSelfCheck 把磁盘与套接字检查移出 UI 线程。
func (s *Service) RunSelfCheck() {
	s.state.Update(func(sn *Snapshot) { sn.Checking = true })
	go func() {
		checks := s.SelfCheck()
		s.state.Update(func(sn *Snapshot) {
			sn.Checks = checks
			sn.Checking = false
		})
	}()
}

// portInUse 报告端口是否已被监听。
//
// 它尝试绑定而不是拨号：在防火墙下，拨号到关闭端口与无人监听表现相同，
// 而绑定失败才回答真正的问题——入站能否使用这个端口？
func portInUse(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return true
	}
	l.Close()
	return false
}
