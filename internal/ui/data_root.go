package ui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/locales"
)

// dataRootForm 保存数据目录切换操作的临时反馈。
type dataRootForm struct {
	errText string
	result  string
}

func (f *dataRootForm) clear() {
	f.errText = ""
	f.result = ""
}

// dataRootOutcome 由工作 goroutine 交回主线程。
type dataRootOutcome struct {
	err      error
	canceled bool
}

// openDataDirectory 打开原生目录选择器。
//
// Dialog.Open 会阻塞，但可以从工作 goroutine 调用；视图线程只接收
// 结果，不会因系统对话框卡住重绘。
func openDataDirectory(parent *mygo.Window, current, title, button string) (string, error) {
	selected, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:            parent,
		Title:             title,
		DefaultPath:       current,
		ButtonLabel:       button,
		Directory:         true,
		CreateDirectories: true,
	})
	if err != nil {
		return "", err
	}
	if len(selected) == 0 {
		return "", nil
	}
	return selected[0], nil
}

// pollDataRootOutcome 在下一帧把工作 goroutine 的结果交给视图。
func (r *Root) pollDataRootOutcome() {
	if r.dataRootResult == nil {
		return
	}
	select {
	case outcome := <-r.dataRootResult:
		r.dataRoot.clear()
		r.dataRootBusy.Store(false)
		if outcome.canceled {
			return
		}
		if outcome.err != nil {
			r.dataRoot.errText = outcome.err.Error()
			return
		}
		r.dataRoot.result = "数据目录已设置，正在重启并迁移文件…"
	default:
	}
}

// finishDataRoot 发布结果，并在成功时请求应用重启。
func (r *Root) finishDataRoot(err error, canceled bool) {
	r.dataRootBusy.Store(false)
	if r.dataRootResult != nil {
		select {
		case r.dataRootResult <- dataRootOutcome{err: err, canceled: canceled}:
		default:
		}
	}
	if err == nil && !canceled && r.relaunch != nil {
		r.relaunch()
	}
}

// chooseDataRoot 异步打开目录选择器，再把选择交给服务写入定位文件。
func (r *Root) chooseDataRoot() {
	snap := r.svc.State().Snapshot()
	if !snap.DataRootEditable || r.dataRootBusy.Load() {
		return
	}
	picker := r.pickDirectory
	if picker == nil {
		r.finishDataRoot(fmt.Errorf("当前环境不支持打开目录选择器"), false)
		return
	}

	r.dataRootBusy.Store(true)
	r.dataRoot.clear()
	parent, current := r.win, snap.DataRoot
	title := locales.Translate(snap.Settings.Language, "选择 vRay 数据目录")
	button := locales.Translate(snap.Settings.Language, "选择")
	go func() {
		target, err := picker(parent, current, title, button)
		if err != nil {
			r.finishDataRoot(fmt.Errorf("选择数据目录失败：%w", err), false)
			return
		}
		if strings.TrimSpace(target) == "" {
			r.finishDataRoot(nil, true)
			return
		}
		err = <-r.svc.SetDataRoot(target)
		r.finishDataRoot(err, false)
	}()
}

// resetDataRoot 异步请求迁回平台默认目录。
func (r *Root) resetDataRoot() {
	snap := r.svc.State().Snapshot()
	if !snap.DataRootEditable || r.dataRootBusy.Load() ||
		snap.DataRootDefault == "" || snap.DataRoot == snap.DataRootDefault {
		return
	}

	r.dataRootBusy.Store(true)
	r.dataRoot.clear()
	go func() {
		err := <-r.svc.ResetDataRoot()
		r.finishDataRoot(err, false)
	}()
}

// dataDirectorySection 显示数据目录，并允许用户选择新的位置。
func (r *Root) dataDirectorySection(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	f := &r.dataRoot
	busy := r.dataRootBusy.Load() || snap.DataRootSaving

	section(c, "数据目录", func() {
		ui.Text(c, tr(c, "节点、订阅、凭据、日志和规则库都保存在这个目录中。")).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, snap.DataRoot).FontSize(12).SingleLine().Tooltip(snap.DataRoot)
		if snap.DataRootDefault != "" && snap.DataRoot != snap.DataRootDefault {
			ui.Text(c, tr(c, "默认目录：")+snap.DataRootDefault).FontSize(11).TextColor(t.TextMuted).SingleLine()
		}
		if snap.DataRootNotice != "" {
			ui.Text(c, snap.DataRootNotice).FontSize(11).TextColor(t.Warning).MaxLines(2)
		}
		ui.Text(c, tr(c, "选择新目录后应用会自动重启，把现有文件复制到新目录；原目录保留，目标目录中的同名文件不会被覆盖。恢复默认目录时，默认目录中的旧数据会先备份到同级目录。")).
			FontSize(11).TextColor(t.TextMuted).MaxLines(3)

		ui.Row(c).Gap(8).Children(func() {
			label := "选择数据目录"
			if busy {
				label = "正在切换…"
			}
			opts := []buttonOption{}
			if !snap.DataRootEditable || busy {
				opts = append(opts, disabled())
			}
			if textButton(c, label, opts...).Clicked() {
				r.chooseDataRoot()
			}
			if snap.DataRootEditable && !busy && snap.DataRootDefault != "" && snap.DataRoot != snap.DataRootDefault {
				if textButton(c, "恢复默认目录").Clicked() {
					r.resetDataRoot()
				}
			}
		})
		if snap.DataRootPending && f.result == "" {
			ui.Text(c, tr(c, "数据目录设置已保存，将在下次启动时迁移。")).FontSize(11).TextColor(t.Success).MaxLines(2)
		}
		if f.result != "" {
			ui.Text(c, tr(c, f.result)).FontSize(11).TextColor(t.Success).MaxLines(2)
		}
		errorText := f.errText
		if errorText == "" {
			errorText = snap.DataRootError
		}
		if errorText != "" {
			ui.Text(c, errorText).FontSize(11).TextColor(t.Danger).MaxLines(3)
		}
	})
}
