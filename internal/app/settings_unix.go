//go:build !windows

package app

import (
	"os"
	"runtime"
)

// tunCheck 报告当前进程能否使用 TUN 模式。
//
// Xray 的 TUN 入站需要创建设备并安装路由，在 POSIX 系统上两者都需要
// root。在自检中说明这一点，用户就能在选择模式并遭遇启动失败前看到答案。
func tunCheck() Check {
	if os.Geteuid() == 0 {
		return Check{Name: "TUN 模式", State: CheckOK, Detail: "以 root 运行，可用"}
	}
	return Check{
		Name:   "TUN 模式",
		State:  CheckWarn,
		Detail: "当前用户不是 root，TUN 需要提权；系统代理模式仍可用（" + runtime.GOOS + "）",
	}
}
