// Command trayprobe 检查 mygo 能否使用此应用自身的图标数据实际注册托盘图标，并报告结果。
//
// 它之所以存在，是因为“托盘图标缺失”可能有多种原因——托盘调用失败、图标转换失败、
// 图标在尚无图像时就被添加——只有实际运行才能区分。
package main

import (
	"fmt"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/shell"
)

func main() {
	mygo.App.WhenReady(func() {
		icon, err := shell.IconBytes(shell.IconIdle())
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取图标失败：%v\n", err)
			mygo.App.Quit()
			return
		}
		fmt.Fprintf(os.Stderr, "图标字节数：%d\n", len(icon))

		win := mygo.NewWindow(mygo.WindowOptions{
			Title:  "托盘探测",
			Width:  320,
			Height: 200,
			Content: ui.View(func(c *ui.Context) {
				ui.Text(c, "托盘探测运行中")
			}),
		})
		_ = win

		tray, err := mygo.NewTray(mygo.TrayOptions{
			ID:      "dev.sbgayhub.vray.trayprobe",
			Icon:    icon,
			Title:   "vRay",
			ToolTip: "vRay 托盘探测",
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{ID: "quit", Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
			}),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "创建托盘失败：%v\n", err)
			mygo.App.Quit()
			return
		}
		fmt.Fprintln(os.Stderr, "托盘创建成功")
		_ = tray
	})

	if err := mygo.App.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "应用运行失败：%v\n", err)
	}
}
