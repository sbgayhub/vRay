// dnssvc 包负责决定 vRay 是否接管系统 DNS，以及如何恢复。
//
// 接管 DNS 是代理客户端最具侵入性的操作：一旦出错，整台机器将完全无法进行域名解析，
// 连代理自身的服务器地址也无法解析。因此设计上将三件事分开：
//
//   - 应用“想要什么”（Policy），它是纯函数且可测试；
//   - 平台“能否做到”（Availability），这是环境事实，而非猜测；
//   - 实际变更，只能通过副作用日志进行。
//
// 本包目前的保证是：vRay 绝不会在未接管 DNS 时声称已接管，也绝不会丢失恢复点。
package dnssvc

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// Policy 表示用户选择的策略。
type Policy string

// 设置页面提供的 DNS 策略。
const (
	// PolicySystem 不修改系统解析器。这是默认值：静默重定向 DNS 的客户端会失去用户信任。
	PolicySystem Policy = "system"
	// PolicyTunnel 通过代理自身的解析器发送查询。
	PolicyTunnel Policy = "tunnel"
	// PolicyDirect 在本地解析，并忽略代理的 DNS。
	PolicyDirect Policy = "direct"
)

// Known 报告当前构建是否理解策略 p。
func (p Policy) Known() bool {
	switch p {
	case PolicySystem, PolicyTunnel, PolicyDirect:
		return true
	default:
		return false
	}
}

// Availability 表示本机的 DNS 能否被接管。
type Availability struct {
	// Can 报告平台机制在当前环境中是否存在。
	Can bool
	// Mechanism 标明将使用的机制，供 UI 展示。
	Mechanism string
	// Why 用用户可据此采取行动的语言解释 Can 为 false 的原因。
	Why string
}

// Decision 是对“是否应接管，以及能否接管？”的回答。
//
// UI 需要的三个答案彼此不同，把它们混为一谈会让客户端在 DNS 问题上撒谎：
// “不会”（用户选择 system）、“不能”（本机没有机制）和“会”（两者均成立）是不同的提示。
type Decision struct {
	Takeover bool
	// 当用户选择 tunnel DNS 但本机无法做到时，Warning 非空，便于 UI 提示查询可能泄漏。
	Warning string
}

// Decide 根据策略和环境计算决策。
func Decide(p Policy, a Availability) Decision {
	if p == PolicySystem {
		return Decision{}
	}
	if !a.Can {
		// 用户要求使用 tunnel DNS，但本机无法做到。诚实的回答是“DNS 未接管，
		// 查询可能泄漏”；静默继续会让人误以为已经生效。
		reason := a.Why
		if reason == "" {
			reason = "本机没有可用的 DNS 接管机制"
		}
		return Decision{Warning: "DNS 未接管（" + reason + "），查询可能泄漏"}
	}
	return Decision{Takeover: true}
}

// Prober 报告本地 DNS 监听器是否确实在某个端口上响应。
//
// 它回答应用真正关心的问题——“我自己的解析器是否已启动？”——而不是“我是否成功绑定端口？”，
// 后者调用方已经知道。
type Prober func(port int) error

// TCPProbe 是默认探测器：向本地端口发起 TCP 连接。
//
// DNS over UDP 是常见情况，但能响应 TCP 的本地解析器必然正在运行；
// 而拨号检查不会一直等待一个永远不会到来的响应。
func TCPProbe(port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("DNS 服务：端口 %d 超出范围", port)
	}
	c, err := net.DialTimeout("tcp",
		net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 500*time.Millisecond)
	if err != nil {
		return fmt.Errorf("DNS 服务：127.0.0.1:%d 无响应：%w", port, err)
	}
	c.Close()
	return nil
}

// ErrUnsupported 是平台层在当前构建无法接管 DNS 时返回的错误。
var ErrUnsupported = errors.New("DNS 服务：当前平台不支持接管 DNS")
