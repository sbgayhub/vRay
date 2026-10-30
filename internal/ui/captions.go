package ui

import (
	"github.com/egoist/mygo/ui"
)

// 标题栏图标。
//
// 这些图标在此处绘制，而不是取自图标集，因为标题栏按钮对通用图标来说太小：
// Crisp UI 的图标为 24x24、描边宽度 2、圆头端点，缩到 10 像素后每条线都会
// 显得模糊粗重，这正是自定义标题栏与系统标题栏并排时显得违和的原因。
//
// 这些图标为 10x10，描边一像素，方形端点，所有坐标都落在半像素上，
// 使线条落在设备像素上，而不是跨越两个像素。
const (
	// glyphMinimise 是一条水平线。
	glyphMinimise = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 10 10">` +
		`<path d="M2 5.5h6" stroke="currentColor" stroke-width="1" fill="none"/>` +
		`</svg>`

	// glyphMaximise 是一个空心方块。
	glyphMaximise = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 10 10">` +
		`<rect x="2.5" y="2.5" width="5" height="5" stroke="currentColor" stroke-width="1" fill="none"/>` +
		`</svg>`

	// glyphRestore 是两个重叠的方块，前面的方块向右下偏移，
	// 与 Windows 绘制最大化窗口还原按钮的方式一致。
	glyphRestore = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 10 10">` +
		`<rect x="2.5" y="2.5" width="5" height="5" stroke="currentColor" stroke-width="1" fill="none"/>` +
		`<path d="M2.5 7.5h5" stroke="currentColor" stroke-width="1" fill="none"/>` +
		`</svg>`

	// glyphClose 是一个叉号。
	glyphClose = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 10 10">` +
		`<path d="M2.5 2.5l5 5M7.5 2.5l-5 5" stroke="currentColor" stroke-width="1" fill="none"/>` +
		`</svg>`
)

// captionGlyphs 只解析一次；ParseSVG 开销不小，而标题栏每帧都会重绘。
var captionGlyphs = func() map[string]*ui.SVG {
	m := map[string]*ui.SVG{}
	for name, data := range map[string]string{
		"minimise": glyphMinimise,
		"maximise": glyphMaximise,
		"restore":  glyphRestore,
		"close":    glyphClose,
	} {
		m[name] = ui.MustParseSVG([]byte(data))
	}
	return m
}()
