package ui

import (
	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/locales"
)

func setUILanguage(c *ui.Context, language string) {
	if language == "" {
		language = "zh-Hans"
	}
	*ui.Local(c.Root(), "vray.language", func() string { return "zh-Hans" }) = language
}

func tr(c *ui.Context, source string) string {
	language := *ui.Local(c.Root(), "vray.language", func() string { return "zh-Hans" })
	return locales.Translate(language, source)
}

func (r *Root) languageButton(c *ui.Context) {
	t := c.Theme()
	r.languageAnchor = ui.ButtonBase(c).Label("切换语言").Tooltip(tr(c, "切换语言")).
		Size(40, captionHeight).Background(ui.Transparent).Center().Children(func() {
		ui.Icon(c, icon.Must("languages")).Size(17, 17).TextColor(t.TextMuted)
	})
	if r.languageAnchor.Hovered() {
		r.languageAnchor.Background(t.SurfaceHover)
	}
	if r.languageAnchor.Clicked() {
		r.languageOpen = !r.languageOpen
	}
}

func (r *Root) languagePopover(c *ui.Context, snap app.Snapshot) {
	if !r.languageOpen {
		return
	}
	r.popover(c, r.languageAnchor, &r.languageOpen, func() {
		ui.Text(c, tr(c, "界面语言")).Bold().FontSize(16)
		for _, language := range []struct{ code, name string }{
			{"zh-Hans", "简体中文"}, {"zh-Hant", "繁體中文"}, {"en", "English"},
		} {
			options := []buttonOption{}
			if snap.Settings.Language == language.code || (snap.Settings.Language == "" && language.code == "zh-Hans") {
				options = append(options, tinted())
			}
			if textButton(c, language.name, options...).FillWidth().Clicked() {
				r.languageSaving = r.svc.SaveLanguage(language.code)
				r.languageOpen = false
			}
		}
	})
}

func (r *Root) applyLanguage(c *ui.Context, snap app.Snapshot) {
	setUILanguage(c, snap.Settings.Language)
	if r.win != nil && r.appliedLanguage != snap.Settings.Language {
		r.appliedLanguage = snap.Settings.Language
		language := snap.Settings.Language
		if language == "" {
			language = "zh-Hans"
		}
		mygo.App.SetLocale(language)
	}
	if r.languageSaving != nil {
		select {
		case <-r.languageSaving:
			r.languageSaving = nil
		default:
			c.Invalidate()
		}
	}
}
