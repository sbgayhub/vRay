// icongen 生成 vRay 的应用图标与托盘图标。
//
// 所有图标都出自同一套几何定义（圆角方形 + 粗笔 V 形 + 状态色），
// 保证应用图标、任务栏图标和托盘四态图标视觉一致。
// 以 4×4 超采样渲染，缩小后边缘平滑，无需外部依赖。
//
// 用法：go run ./tools/icongen -out <仓库根目录>
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// supersample 是每条边的采样倍数，16 个子采样点足以消除锯齿。
const supersample = 4

// rgb 构造一个不透明的颜色。
func rgb(r, g, b uint8) color.RGBA {
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

// palette 是一个图标使用的渐变端点与前景色。
type palette struct {
	from, to color.RGBA
	mark     color.RGBA
}

var (
	// 应用与品牌托盘：深空蓝到天蓝的对角渐变，白色标记。
	brandPal = palette{from: rgb(30, 58, 158), to: rgb(14, 165, 233), mark: rgb(255, 255, 255)}
	// 四种运行状态：灰（未连接）、绿（已连接）、琥珀（连接中）、红（失败）。
	idlePal     = palette{from: rgb(71, 85, 105), to: rgb(100, 116, 139), mark: rgb(255, 255, 255)}
	activePal   = palette{from: rgb(21, 128, 61), to: rgb(34, 197, 94), mark: rgb(255, 255, 255)}
	startingPal = palette{from: rgb(217, 119, 6), to: rgb(245, 158, 11), mark: rgb(255, 255, 255)}
	failedPal   = palette{from: rgb(220, 38, 38), to: rgb(239, 68, 68), mark: rgb(255, 255, 255)}
	// macOS 模板托盘：纯黑形状，系统负责随菜单栏着色。
	templatePal = palette{from: rgb(0, 0, 0), to: rgb(0, 0, 0), mark: rgb(0, 0, 0)}
)

func main() {
	out := flag.String("out", ".", "仓库根目录，图标写入其 resources 与 internal/shell/icons")
	flag.Parse()
	root := *out

	// 应用图标：1024²，圆角方形 + V 形 + 顶点光点 + 左上高光。
	if err := writeIcon(filepath.Join(root, "resources", "icon.png"), 1024, brandPal, badgeOptions{
		radius:  0.225,
		stroke:  0.085,
		highlit: true,
		dot:     true,
	}); err != nil {
		exit(err)
	}
	// Windows/Linux 彩色托盘图标：与品牌一致。
	if err := writeIcon(filepath.Join(root, "resources", "tray.png"), 32, brandPal, badgeOptions{
		radius: 0.24,
		stroke: 0.13,
	}); err != nil {
		exit(err)
	}
	// macOS 模板托盘：黑色轮廓 + 黑色 V。
	if err := writeIcon(filepath.Join(root, "resources", "tray-template.png"), 32, templatePal, badgeOptions{
		radius:  0.24,
		stroke:  0.13,
		outline: true,
	}); err != nil {
		exit(err)
	}
	// 托盘四态。
	states := []struct {
		name string
		pal  palette
	}{
		{"tray-idle", idlePal},
		{"tray-active", activePal},
		{"tray-starting", startingPal},
		{"tray-failed", failedPal},
	}
	for _, st := range states {
		path := filepath.Join(root, "internal", "shell", "icons", st.name+".png")
		if err := writeIcon(path, 32, st.pal, badgeOptions{
			radius: 0.24,
			stroke: 0.13,
		}); err != nil {
			exit(err)
		}
	}
	fmt.Println("图标已生成：resources/icon.png、tray.png、tray-template.png、internal/shell/icons/*.png")
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, "生成图标失败：", err)
	os.Exit(1)
}

// badgeOptions 描述一枚徽章图标的几何参数，比例均为边长的占比。
type badgeOptions struct {
	radius  float64 // 圆角半径占比
	stroke  float64 // V 形笔画宽度占比
	dot     bool    // 是否绘制顶点光点
	highlit bool    // 是否绘制左上高光斜带
	outline bool    // 只描边不填充（macOS 模板图标）
}

// writeIcon 渲染一枚图标并写为 PNG。
func writeIcon(path string, size int, pal palette, opts badgeOptions) error {
	img := renderBadge(size, pal, opts)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// renderBadge 以超采样渲染徽章：圆角渐变底 + 白色 V 形标记。
func renderBadge(size int, pal palette, opts badgeOptions) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, size, size))

	// V 形的两条骨架：左上臂、右下顶点、右上臂。
	armY, midY, topInset := 0.27, 0.72, 0.20
	left := pt{topInset, armY}
	apex := pt{0.5, midY}
	right := pt{1 - topInset, armY}
	half := opts.stroke / 2

	// 顶点光点：位于 V 形开口的中央上方，像一颗信号源。
	dotC := pt{0.5, 0.40}
	dotR := opts.stroke * 0.62

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			// 在输出像素内取 4×4 个子采样，统计覆盖并混合颜色。
			var accR, accG, accB, accA float64
			for sy := 0; sy < supersample; sy++ {
				for sx := 0; sx < supersample; sx++ {
					u := (float64(x) + (float64(sx)+0.5)/supersample) / float64(size)
					v := (float64(y) + (float64(sy)+0.5)/supersample) / float64(size)
					c, a := shade(u, v, pal, opts, left, apex, right, half, dotC, dotR)
					accR += float64(c.R) * a
					accG += float64(c.G) * a
					accB += float64(c.B) * a
					accA += a
				}
			}
			n := float64(supersample * supersample)
			if accA == 0 {
				continue
			}
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(math.Round(accR / accA)),
				G: uint8(math.Round(accG / accA)),
				B: uint8(math.Round(accB / accA)),
				A: uint8(math.Round(accA / n * 255)),
			})
		}
	}
	return out
}

// shade 返回子采样点 (u,v) 处的颜色与覆盖率（0 到 1）。
//
// 内部使用以中心为原点的 [-0.5,0.5] 坐标，便于表达圆角与笔画。
func shade(u, v float64, pal palette, opts badgeOptions, left, apex, right pt, half float64, dotC pt, dotR float64) (color.RGBA, float64) {
	x, y := u-0.5, v-0.5

	if !inRoundedRect(x, y, 0.5, opts.radius) {
		return color.RGBA{}, 0
	}
	if opts.outline {
		// 模板图标：只保留圆角边框，V 形在边框内保持实心。
		border := 0.07
		if inRoundedRect(x, y, 0.5-border, opts.radius*0.72) {
			if distToSeg(x, y, left, apex) <= half || distToSeg(x, y, apex, right) <= half {
				return pal.mark, 1
			}
			return color.RGBA{}, 0
		}
		return pal.mark, 1
	}

	// 底色：沿对角线插值的渐变。
	base := lerp(pal.from, pal.to, clamp01((u+v)/2))

	// V 形标记：两条粗线段的并集，笔画端点自然圆头。
	if distToSeg(x, y, left, apex) <= half || distToSeg(x, y, apex, right) <= half {
		return pal.mark, 1
	}
	if opts.dot && dist(x, y, dotC) <= dotR {
		return pal.mark, 1
	}
	if opts.highlit {
		// 左上高光：一条柔和的斜向亮带，增加体积感。
		band := (x + y*0.6 + 0.5) / 1.1
		if band > 0.18 && band < 0.30 {
			k := 1 - math.Abs((band-0.24)/0.06)
			return lerp(base, rgb(255, 255, 255), 0.10*k), 1
		}
	}
	return base, 1
}

// pt 是单位坐标系（0 到 1）中的一个点。
type pt struct{ x, y float64 }

// dist 返回 (x,y) 到单位坐标点 p 的距离。
func dist(x, y float64, p pt) float64 {
	return math.Hypot(x-(p.x-0.5), y-(p.y-0.5))
}

// distToSeg 返回点到线段 ab 的最短距离。
func distToSeg(x, y float64, a, b pt) float64 {
	ax, ay := a.x-0.5, a.y-0.5
	bx, by := b.x-0.5, b.y-0.5
	dx, dy := bx-ax, by-ay
	ll := dx*dx + dy*dy
	if ll == 0 {
		return math.Hypot(x-ax, y-ay)
	}
	t := clamp01(((x-ax)*dx + (y-ay)*dy) / ll)
	return math.Hypot(x-(ax+t*dx), y-(ay+t*dy))
}

// inRoundedRect 判断点是否在以原点为中心、半边长为 h、圆角半径为 r 的圆角方形内。
func inRoundedRect(x, y, h, r float64) bool {
	ax, ay := math.Abs(x), math.Abs(y)
	if ax <= h-r || ay <= h-r {
		return ax <= h && ay <= h
	}
	dx, dy := ax-(h-r), ay-(h-r)
	return dx*dx+dy*dy <= r*r
}

// lerp 在两个颜色之间按 t 插值。
func lerp(a, b color.RGBA, t float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: 255,
	}
}

// clamp01 把数值限制在 [0,1]。
func clamp01(t float64) float64 {
	return math.Max(0, math.Min(1, t))
}
