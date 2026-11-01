// Package core 在 vRay 进程内运行 Xray。
//
// 这里的 Xray 是库而不是子进程：core.New 与 Instance.Start 都在本进程内
// 执行。这意味着内核里的 panic 会连同 UI 一起拖垮，
// 因此每次进入内核的调用都经过 recover。
package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/xtls/xray-core/core"

	// 只注册客户端需要的能力。
	_ "github.com/sbgayhub/vray/internal/core/distro"
)

// Phase 表示内核所处的生命周期阶段。
type Phase string

// 内核阶段的取值。
const (
	PhaseIdle      Phase = "idle"
	PhasePreflight Phase = "preflight"
	PhaseStarting  Phase = "starting"
	PhaseRunning   Phase = "running"
	PhaseStopping  Phase = "stopping"
	PhaseFailed    Phase = "failed"
)

// Code 对内核失败原因分类，供 UI 生成错误文案。
type Code string

// 供 UI 索引文案的问题代码。
const (
	CodeConfigInvalid  Code = "CONFIG_INVALID"
	CodeStartFailed    Code = "CORE_START_FAILED"
	CodeNotReady       Code = "CORE_NOT_READY"
	CodeAlreadyRunning Code = "CORE_ALREADY_RUNNING"
	CodePanic          Code = "CORE_PANIC"
	CodeCloseFailed    Code = "CORE_CLOSE_FAILED"
)

// Problem 是 UI 可以展示的失败信息。
type Problem struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

// Error 实现 error 接口。
func (p *Problem) Error() string {
	if p.Detail == "" {
		return fmt.Sprintf("%s: %s", p.Code, p.Message)
	}
	return fmt.Sprintf("%s: %s (%s)", p.Code, p.Message, p.Detail)
}

// Engine 持有 Xray 实例：校验配置、启动、报告可用状态并停止。
// 它可安全地并发使用。
type Engine struct {
	policyCancel    func()
	policyDone      chan struct{}
	policyRoute     configgen.PolicyRoute
	certificateMu   sync.Mutex
	certificatePins map[string]string
	connections     *connectionTracker
	mu              sync.Mutex
	inst            *core.Instance
	phase           Phase
	prob            *Problem
	since           time.Time

	// load 在启动前校验配置。做成字段是为了让测试替换成会 panic 的版本，
	// 以证明 Start 周围的 recover 有效；生产环境始终使用
	// core.LoadConfig。
	load func(cfg []byte) (*core.Config, error)

	// logger 非空时，在每个实例启动后接收内核日志。使用接口是为了让本包
	// 不依赖日志收集器，测试也能传入桩实现。
	logger LogAttacher
}

// LogAttacher 是能接管进程级日志处理器的对象。
//
// Attach 必须在实例启动之后调用，绝不能提前：Xray 的 app/log 在构建配置时
// 会注册自己的处理器并覆盖已安装的处理器（见 internal/core/logs）。
type LogAttacher interface {
	Attach()
}

// SetLogger 让引擎在每次启动实例时把日志收集交给 l。启动时调用一次即可。
func (e *Engine) SetLogger(l LogAttacher) {
	e.mu.Lock()
	e.logger = l
	e.mu.Unlock()
}

// New 返回一个空闲状态的内核。
func New() *Engine {
	return &Engine{phase: PhaseIdle, since: time.Now(), connections: &connectionTracker{}}
}

// State 返回当前阶段，失败时同时返回问题详情。
func (e *Engine) State() (Phase, *Problem) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.phase, e.prob
}

// loader 返回配置校验函数，默认使用真实实现。
func (e *Engine) loader() func([]byte) (*core.Config, error) {
	if e.load != nil {
		return e.load
	}
	return func(cfg []byte) (*core.Config, error) {
		return core.LoadConfig("json", bytes.NewReader(cfg))
	}
}

// Running 返回实例是否已启动。
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inst != nil
}

// Start 校验 cfg 并用它启动 Xray。
//
// 所有失败都返回 *Problem 而非裸 error，以便 UI 按代码索引文案。
func (e *Engine) Start(cfg []byte) (err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.inst != nil {
		return &Problem{Code: CodeAlreadyRunning, Message: "内核已在运行"}
	}

	var pending *core.Instance
	// 已构造但尚未启动成功的实例由本函数持有。
	defer func() {
		if pending != nil {
			// 清理不得掩盖最初的启动问题或 panic。
			func() { defer func() { _ = recover() }(); _ = pending.Close() }()
			e.connections.reset()
		}
	}()
	// 下面任何 panic 都是 Xray 的问题，不是 UI 的。
	defer func() {
		if r := recover(); r != nil {
			e.inst = nil
			e.phase = PhaseFailed
			e.prob = &Problem{
				Code:    CodePanic,
				Message: "内核在启动时崩溃",
				Detail:  fmt.Sprint(r),
			}
			err = e.prob
		}
	}()

	// 预检：在进程内校验，等价于 `xray -test`（P6）。
	e.phase, e.prob, e.since = PhasePreflight, nil, time.Now()
	parsed, loadErr := e.loader()(cfg)
	if loadErr != nil {
		e.fail(CodeConfigInvalid, "配置校验失败", loadErr)
		return e.prob
	}
	var mode struct {
		Inbounds []struct {
			Protocol string `json:"protocol"`
		} `json:"inbounds"`
	}
	if json.Unmarshal(cfg, &mode) == nil {
		for _, in := range mode.Inbounds {
			if in.Protocol == "tun" {
				if err := e.CheckMode("tun"); err != nil {
					e.fail(CodeStartFailed, "TUN 启动条件未满足", err)
					return e.prob
				}
			}
		}
	}

	e.phase, e.since = PhaseStarting, time.Now()
	// 复用已校验的 protobuf。StartInstance 会重新加载 JSON 并再次构建全部
	// Geo 匹配器，产生大量垃圾内存。
	inst, startErr := core.New(parsed)
	if startErr != nil {
		e.fail(CodeStartFailed, "内核启动失败", startErr)
		return e.prob
	}
	pending = inst
	if startErr = e.trackConnections(inst); startErr != nil {
		e.fail(CodeStartFailed, "连接监测初始化失败", startErr)
		return e.prob
	}
	if startErr = inst.Start(); startErr != nil {
		e.fail(CodeStartFailed, "内核启动失败", startErr)
		return e.prob
	}

	e.inst = inst
	e.startPolicies(inst, cfg)
	e.phase, e.since = PhaseRunning, time.Now()

	// 此时接管日志处理器才安全：Xray 的 app/log 在构建配置时会替换处理器，
	// 提前接管将收集不到任何日志。
	if e.logger != nil {
		e.logger.Attach()
	}
	pending = nil
	return nil
}

// Stop 关闭 Xray。它可重复调用：停止空闲内核同样合法。
func (e *Engine) Stop() (err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.connections.reset()

	if e.inst == nil {
		e.phase, e.prob, e.since = PhaseIdle, nil, time.Now()
		return nil
	}

	defer func() {
		if r := recover(); r != nil {
			e.fail(CodePanic, "内核在停止时崩溃", fmt.Sprint(r))
			err = e.prob
		}
	}()

	e.phase, e.since = PhaseStopping, time.Now()
	if e.policyCancel != nil {
		e.policyCancel()
		<-e.policyDone
		e.policyCancel = nil
		e.policyDone = nil
	}
	closeErr := e.inst.Close() // 幂等操作
	e.inst = nil

	if closeErr != nil {
		e.fail(CodeCloseFailed, "内核停止失败", closeErr)
		return e.prob
	}
	e.phase, e.prob, e.since = PhaseIdle, nil, time.Now()
	return nil
}

// fail 记录问题以及内核停留的阶段。
func (e *Engine) fail(code Code, msg string, cause any) {
	e.phase = PhaseFailed
	e.prob = &Problem{Code: code, Message: msg, Detail: detailOf(cause)}
}

// detailOf 截取原因的首行，并限制长度以便 UI 展示。
func detailOf(cause any) string {
	s := fmt.Sprint(cause)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
