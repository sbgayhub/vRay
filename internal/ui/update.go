package ui

import (
	"fmt"
	"strings"

	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

const releasesURL = "https://github.com/sbgayhub/vray/releases"

func appVersion() string {
	if version := mygo.App.Version(); version != "" {
		return version
	}
	return "0.0.0-dev"
}

// updateTitleButton 仅在签名更新器存在可操作状态时显示。首次点击会立即开始下载和校验；
// 暂存完成后，同一按钮会变为明确的重启操作。
func (r *Root) updateTitleButton(c *ui.Context, snap app.Snapshot) {
	update := snap.Update
	if snap.ConsentRequired || update.AvailableVersion == "" {
		return
	}
	switch {
	case update.Installed:
		if textButton(c, "重启更新", primary()).Height(28).FontSize(11).
			Tooltip(tr(c, "更新已下载并校验完成，重启应用后生效。")).Clicked() {
			r.svc.RelaunchForUpdate()
		}
	case update.Installing:
		label := "更新中…"
		if update.Total > 0 {
			label = fmt.Sprintf(tr(c, "更新 %d%%"), min(100, int(update.Downloaded*100/update.Total)))
		}
		if textButton(c, label, tinted()).Height(28).FontSize(11).
			Tooltip(tr(c, "正在安装更新…")).Clicked() {
			r.updateOpen = true
		}
	default:
		if textButton(c, fmt.Sprintf(tr(c, "更新 v%s"), update.AvailableVersion), primary()).Height(28).FontSize(11).
			Tooltip(tr(c, "点击下载、校验并准备更新；完成后可重启应用")).Clicked() {
			r.updateOpen = true
			r.svc.InstallUpdate()
		}
	}
}

func (r *Root) updateSettingsSection(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	f := &r.settingsF
	section(c, "软件更新", func() {
		ui.Checkbox(c, &f.autoUpdate, tr(c, "每天 04:00 自动检查更新"))
		ui.Text(c, tr(c, "自动检查只查询已签名的发布版本；发现新版本后仍由你确认安装。")).
			FontSize(11).TextColor(t.TextMuted).MaxLines(2)
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr(c, "当前版本 ")+appVersion()).FontSize(12).Grow(1)
			status, colour := updateStatus(c, snap.Update)
			ui.Text(c, tr(c, status)).FontSize(11).TextColor(colour).SingleLine()
		})
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			opts := []buttonOption{}
			if snap.Update.Checking || snap.Update.Installing {
				opts = append(opts, disabled())
			}
			label := "立即检查"
			if snap.Update.Checking {
				label = "检查中…"
			}
			if textButton(c, label, opts...).Clicked() {
				r.updateOpen = true
				r.svc.CheckForUpdates()
			}
			if snap.Update.AvailableVersion != "" {
				if textButton(c, "查看更新", primary()).Clicked() {
					r.updateOpen = true
				}
			}
			if !snap.Update.Enabled {
				if textButton(c, "打开发布页").Clicked() {
					c.OpenURL(releasesURL)
				}
			}
			ui.Spacer(c)
			if !snap.Update.LastCheckedAt.IsZero() {
				ui.Text(c, tr(c, "上次检查 ")+snap.Update.LastCheckedAt.Format("15:04")).
					FontSize(10).TextColor(t.TextMuted)
			}
		})
	})
}

func updateStatus(c *ui.Context, update app.UpdateView) (string, ui.Color) {
	t := c.Theme()
	switch {
	case update.Installing:
		return tr(c, "正在安装更新…"), t.TextMuted
	case update.Checking:
		return tr(c, "正在检查更新…"), t.TextMuted
	case update.Error != "":
		return tr(c, "更新失败"), t.Danger
	case update.Installed:
		return tr(c, "更新已就绪，重启后生效"), t.Success
	case update.AvailableVersion != "":
		return tr(c, "发现新版本 v") + update.AvailableVersion, t.Accent
	case update.UpToDate:
		return tr(c, "已是最新版本"), t.Success
	default:
		return tr(c, "尚未检查"), t.TextMuted
	}
}

func (r *Root) updateDialog(c *ui.Context, snap app.Snapshot) {
	if !r.updateOpen {
		return
	}
	r.modal(c, "软件更新", &r.updateOpen, func() {
		t := c.Theme()
		update := snap.Update
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(42, 42).Shrink(0).Radius(14).Center().Background(t.Accent.Alpha(.12)).Children(func() {
				ui.Icon(c, icon.Must("download")).Size(21, 21).TextColor(accentForeground(t))
			})
			ui.Column(c).Grow(1).Basis(0).Gap(4).Children(func() {
				ui.Text(c, tr(c, "vRay ")+appVersion()).Bold().FontSize(15)
				status, colour := updateStatus(c, update)
				ui.Text(c, tr(c, status)).FontSize(12).TextColor(colour)
			})
		})

		switch {
		case update.AvailableVersion != "":
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "v"+update.AvailableVersion).Bold().FontSize(22)
				if !update.ReleaseDate.IsZero() {
					ui.Text(c, tr(c, "发布于 ")+update.ReleaseDate.Local().Format("2006-01-02")).
						FontSize(11).TextColor(t.TextMuted)
				}
			})
			ui.Scroll(c).FillWidth().MaxHeight(220).Children(func() {
				notes := strings.TrimSpace(update.Notes)
				if notes == "" {
					notes = tr(c, "该版本未提供发布说明。")
				}
				ui.Text(c, notes).FontSize(12).TextColor(t.TextMuted)
			})
		case update.Error != "":
			ui.Box(c).FillWidth().Radius(12).Padding(12).Background(t.Danger.Alpha(.08)).Children(func() {
				ui.Text(c, localizedUpdateError(c, update.Error)).FontSize(12).MaxLines(4).TextColor(t.Danger)
			})
		case update.UpToDate:
			ui.Text(c, tr(c, "你正在使用最新版本。")).FontSize(13).TextColor(t.Success)
		case !update.Enabled:
			ui.Text(c, tr(c, "当前构建或安装位置不支持自动更新。可从 GitHub Releases 手动下载。")).
				FontSize(12).TextColor(t.TextMuted).MaxLines(3)
		default:
			ui.Text(c, tr(c, "正在检查已签名的发布版本…")).FontSize(12).TextColor(t.TextMuted)
		}

		if update.Installing || update.Installed {
			progress := float64(0)
			if update.Total > 0 {
				progress = min(1, float64(update.Downloaded)/float64(update.Total))
			}
			if update.Installed {
				progress = 1
			}
			ui.Progress(c, progress).FillWidth().Height(6)
			label := transferProgress(update.Downloaded, update.Total)
			if update.Installing && update.Total == 0 {
				label = tr(c, "正在连接更新服务器…")
			}
			if update.Installed {
				label = tr(c, "更新已下载并校验完成，重启应用后生效。")
			}
			ui.Text(c, tr(c, label)).FontSize(10).TextColor(t.TextMuted)
		}

		if update.Notes != "" && update.AvailableVersion == "" {
			ui.Text(c, update.Notes).FontSize(11).TextColor(t.TextMuted).MaxLines(4)
		}

		ui.Divider(c)
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if update.Installed {
				if textButton(c, "立即重启", primary()).Clicked() {
					r.svc.RelaunchForUpdate()
				}
			} else if update.AvailableVersion != "" {
				opts := []buttonOption{primary()}
				if update.Installing || update.Checking {
					opts = append(opts, disabled())
				}
				label := "安装更新"
				if update.Installing {
					label = "正在安装…"
				}
				if textButton(c, label, opts...).Clicked() {
					r.svc.InstallUpdate()
				}
			} else {
				opts := []buttonOption{primary()}
				if update.Checking || update.Installing {
					opts = append(opts, disabled())
				}
				if textButton(c, "立即检查", opts...).Clicked() {
					r.svc.CheckForUpdates()
				}
			}
			if !update.Enabled {
				if textButton(c, "打开发布页").Clicked() {
					c.OpenURL(releasesURL)
				}
			}
			ui.Spacer(c)
			if textButton(c, "稍后").Clicked() {
				r.updateOpen = false
			}
		})
	})
}

func transferProgress(downloaded, total int64) string {
	if total <= 0 {
		return fmt.Sprintf("%.1f MiB", float64(downloaded)/(1<<20))
	}
	return fmt.Sprintf("%.1f / %.1f MiB", float64(downloaded)/(1<<20), float64(total)/(1<<20))
}

func localizedUpdateError(c *ui.Context, message string) string {
	if strings.HasPrefix(message, "更新失败：") {
		return fmt.Sprintf(tr(c, "更新失败：%s"), strings.TrimPrefix(message, "更新失败："))
	}
	return tr(c, message)
}
