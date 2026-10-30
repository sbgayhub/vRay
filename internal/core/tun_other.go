//go:build !windows

package core

import (
	"fmt"
	"os"
)

func (e *Engine) CheckMode(mode string) error {
	if mode == "tun" && os.Geteuid() != 0 && os.Getenv("XRAY_TUN_FD") == "" {
		return fmt.Errorf("TUN 需要管理员权限或已授权的 TUN 文件描述符")
	}
	return nil
}
