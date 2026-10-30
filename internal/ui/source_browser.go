package ui

import (
	"fmt"
	"strconv"

	"github.com/ELY-Open-Source/CrychicUI/form"
	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

type sourceForm struct {
	open, saving bool
	waiting      string
	draft        app.SourceEdit
	interval     string
}

func (r *Root) sources(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(20).Children(func() {
		r.importResult(c, snap)
		if snap.SourceResult != "" {
			ui.Text(c, snap.SourceResult).FontSize(12).TextColor(t.TextMuted)
		}
		if len(snap.Sources) == 0 {
			r.emptyState(c, "还没有订阅", "添加一个订阅来源，在这里查看节点、更新状态与剩余配额。")
			return
		}
		var nodes int
		var quota, remaining int64
		for _, src := range snap.Sources {
			nodes += src.NodeCount
			if src.Total > 0 {
				quota += src.Total
				remaining += max(0, src.Total-src.Upload-src.Download)
			}
		}
		panel(c, func() {
			ui.Row(c).FillWidth().Gap(24).Children(func() {
				ui.Column(c).Grow(1).Gap(6).Children(func() {
					ui.Text(c, tr(c, "订阅节点")).FontSize(11).TextColor(t.TextMuted)
					ui.Text(c, fmt.Sprint(nodes)).FontSize(25).Bold()
				})
				ui.Column(c).Grow(1).Gap(6).Children(func() {
					ui.Text(c, tr(c, "可用配额")).FontSize(11).TextColor(t.TextMuted)
					value := "未提供"
					if quota > 0 {
						value = transferLabel(remaining)
					}
					ui.Text(c, tr(c, value)).FontSize(25).Bold()
				})
			})
		})
		width, _ := c.Size()
		for start := 0; start < len(snap.Sources); {
			count := 1
			if width >= 1050 && start+1 < len(snap.Sources) {
				count = 2
			}
			ui.Row(c).FillWidth().Gap(18).AlignItems(ui.Stretch).Children(func() {
				for _, src := range snap.Sources[start : start+count] {
					c.Key(src.ID)
					ui.Column(c).Grow(1).Basis(0).Children(func() { r.sourceCard(c, src) })
				}
			})
			start += count
		}
	})
}

func (r *Root) sourceCard(c *ui.Context, src app.SourceView) {
	t := c.Theme()
	panel(c, func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(40, 40).Radius(13).Center().Background(t.Accent.Alpha(0.1)).Children(func() {
				ui.Icon(c, icon.Must("download")).Size(20, 20).TextColor(t.Accent)
			})
			ui.Column(c).Grow(1).Basis(0).Gap(5).Children(func() {
				ui.Text(c, src.Name).Bold().FontSize(16).SingleLine()
				host := src.Host
				if host == "" {
					host = tr(c, "自定义订阅")
				}
				ui.Text(c, host).FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
			status, color := "已启用", t.Success
			if !src.Enabled {
				status, color = "已停用", t.TextMuted
			}
			if src.Updating {
				status, color = "更新中", t.Accent
			}
			ui.Text(c, tr(c, status)).FontSize(11).TextColor(color)
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf(tr(c, "%d 个节点"), src.NodeCount)).Bold()
			ui.Spacer(c)
			if !src.ExpireAt.IsZero() {
				ui.Text(c, src.ExpireAt.Format("2006-01-02")+tr(c, " 到期")).FontSize(11).TextColor(t.TextMuted)
			}
		})
		if src.Total > 0 {
			used := src.Upload + src.Download
			ui.Progress(c, min(1, float64(used)/float64(src.Total))).FillWidth().Height(5)
			ui.Text(c, transferLabel(used)+" / "+transferLabel(src.Total)).FontSize(12).TextColor(t.TextMuted)
		} else {
			ui.Text(c, tr(c, "订阅未提供流量配额")).FontSize(12).TextColor(t.TextMuted)
		}
		ui.Row(c).FillWidth().Gap(16).Children(func() {
			ui.Text(c, "↑ "+transferLabel(src.Upload)).FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, "↓ "+transferLabel(src.Download)).FontSize(11).TextColor(t.TextMuted)
		})
		updated := tr(c, "尚未更新")
		if !src.LastFetchedAt.IsZero() {
			updated = tr(c, "更新于 ") + src.LastFetchedAt.Format("01-02 15:04")
		}
		ui.Text(c, updated).FontSize(11).TextColor(t.TextMuted)
		if src.LastError != "" {
			ui.Text(c, tr(c, "上次更新失败：")+src.LastError).FontSize(11).TextColor(t.Warning).MaxLines(2)
		}
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if src.Updating {
				textButton(c, "更新中…", disabled())
			} else if textButton(c, "更新").Label("更新订阅 " + src.Name).Clicked() {
				r.svc.RefreshSource(src.ID)
			}
			if textButton(c, "编辑").Label("编辑订阅 " + src.Name).Clicked() {
				r.sourceF = sourceForm{open: true, waiting: src.ID}
				r.svc.EditSource(src.ID)
			}
			ui.Spacer(c)
			if textButton(c, "删除", dangerous()).Label("删除订阅 " + src.Name).Clicked() {
				r.sourceDeleting = src.ID
				r.sourceRemoveNodes = false
			}
		})
	})
}

func (r *Root) sourceDialog(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	f := &r.sourceF
	if f.open && f.waiting != "" && snap.SourceDraft != nil && snap.SourceDraft.ID == f.waiting {
		f.draft, f.waiting = *snap.SourceDraft, ""
		f.interval = strconv.Itoa(f.draft.IntervalMinutes)
	}
	if f.saving && snap.SourceResult != "" {
		f.saving = false
		if snap.SourceDraft == nil && snap.SourceResult == "订阅已保存" {
			f.open = false
			f.draft.URL = ""
		}
	}
	if f.open {
		r.modal(c, "编辑订阅", &f.open, func() {
			if f.waiting != "" {
				ui.Text(c, tr(c, "读取订阅…")).TextColor(t.TextMuted)
				return
			}
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				fieldHeight := float32(265)
				if r.renderingPopup {
					fieldHeight = max(fieldHeight, r.popupLayoutHeight-220)
				}
				ui.Scroll(c).FillWidth().MaxHeight(fieldHeight).Children(func() {
					ui.Column(c).FillWidth().Gap(12).Children(func() {
						form.FieldGroup(c, tr(c, "订阅名称"), form.FieldGroupOptions{}, func() { ui.TextInput(c, &f.draft.Name).FillWidth() })
						form.FieldGroup(c, tr(c, "订阅链接"), form.FieldGroupOptions{}, func() { ui.TextInput(c, &f.draft.URL).FillWidth() })
						form.FieldGroup(c, "User-Agent", form.FieldGroupOptions{Help: tr(c, "留空使用 vRay 默认值")}, func() { ui.TextInput(c, &f.draft.UserAgent).FillWidth() })
						form.FieldGroup(c, tr(c, "自动更新间隔（分钟）"), form.FieldGroupOptions{Help: tr(c, "0 为仅手动更新；自动更新最小 15 分钟")}, func() { ui.TextInput(c, &f.interval).Width(120) })
						ui.Checkbox(c, &f.draft.Enabled, tr(c, "启用订阅"))
					})
				})
				if snap.SourceResult != "" {
					ui.Text(c, snap.SourceResult).FontSize(11).TextColor(t.Warning).MaxLines(2)
				}
				ui.Row(c).Gap(8).Children(func() {
					if f.saving {
						textButton(c, "保存中…", disabled())
					} else if textButton(c, "保存订阅", primary()).Clicked() {
						interval, err := strconv.Atoi(f.interval)
						if err != nil {
							f.draft.IntervalMinutes = -1
						} else {
							f.draft.IntervalMinutes = interval
						}
						f.saving = true
						r.svc.SaveSource(f.draft)
					}
					if textButton(c, "取消").Clicked() {
						f.open = false
						f.draft.URL = ""
						r.svc.CloseSourceEditor()
					}
				})
			})
		})
	}
	if r.sourceDeleting != "" {
		open := true
		r.modal(c, "删除订阅", &open, func() {
			ui.Column(c).FillWidth().Gap(16).Children(func() {
				ui.Text(c, tr(c, "移除此订阅来源？默认保留节点，并归入手动导入。")).FontSize(12)
				ui.Checkbox(c, &r.sourceRemoveNodes, tr(c, "同时删除该订阅的节点"))
				if snap.SourceResult != "" {
					ui.Text(c, snap.SourceResult).FontSize(11).TextColor(t.Warning).MaxLines(2)
				}
				ui.Row(c).Gap(8).Children(func() {
					if textButton(c, "确认删除订阅", dangerous()).Clicked() {
						r.svc.DeleteSource(r.sourceDeleting, r.sourceRemoveNodes)
						r.sourceDeleting = ""
					}
					if textButton(c, "取消").Clicked() {
						r.sourceDeleting = ""
					}
				})
			})
		})
		if !open {
			r.sourceDeleting = ""
		}
	}
}
