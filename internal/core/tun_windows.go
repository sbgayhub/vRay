//go:build windows

package core

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func (e *Engine) CheckMode(mode string) error {
	if mode != "tun" {
		return nil
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("TUN 需要管理员权限，请以管理员身份运行 vRay 后重试")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "wintun.dll")); err != nil {
		return fmt.Errorf("找不到 wintun.dll，请将便携包中的驱动文件放在程序旁边")
	}
	return nil
}
