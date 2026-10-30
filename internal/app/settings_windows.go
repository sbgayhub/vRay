//go:build windows

package app

// tunCheck 检查 Windows 上的 TUN 模式。
//
// Windows 没有 root：TUN 入站需要管理员令牌，普通启动的进程没有。
// 在这里检测令牌意味着为了一行提示链接整套 Windows API，因此检查只报告
// 它确实知道的事实——TUN 需要提权——而不是猜测令牌状态，并在常见的
// “以管理员身份运行”场景中猜错。
func tunCheck() Check {
	return Check{
		Name:   "TUN 模式",
		State:  CheckWarn,
		Detail: "TUN 需要管理员权限启动 vRay；系统代理模式无需提权",
	}
}
