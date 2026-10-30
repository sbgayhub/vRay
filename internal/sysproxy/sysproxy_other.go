//go:build !windows

package sysproxy

import "errors"

// New 返回当前平台的后端。
//
// macOS 按网络服务使用 `networksetup`；Linux 桌面使用 gsettings。这里尚未实现
// 二者：由从未在这些系统上运行过的程序写入系统设置，正是客户端破坏用户网络的
// 方式；而一个报告“不支持”的未实现后端，比猜测更诚实。接口、日志和恢复路径均
// 已就位，缺少的只是这两组命令序列。
func New() Backend { return unsupported{} }

// unsupported 是 vRay 不设置代理的平台后端。
type unsupported struct{}

var errUnsupported = errors.New("系统代理：当前平台暂不支持设置系统代理")

// Enable 实现 Backend。
func (unsupported) Enable(Settings) (Previous, error) { return Previous{}, errUnsupported }

// Restore 实现 Backend。在未做任何修改的平台上无需恢复，因此来自另一台机器的
// 日志条目会被忽略，而不是让整个恢复过程失败。
func (unsupported) Restore(Previous) error { return nil }

// Current 实现 Backend。
func (unsupported) Current() (Previous, error) { return Previous{}, errUnsupported }
