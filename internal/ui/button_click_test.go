package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 一个最小实验：只带 Label 的 ButtonBase 会报告点击吗？
//
// 实测答案：不会。`Label()` 只设置无障碍名称，因此没有子元素的按钮不绘制任何内容，
// 测量尺寸为零；而点击需要指针落在宽度非零的框内。这正是导致设置按钮失效的事实，
// 也是本包所有按钮都经由 textButton 创建（它会在内部绘制标签）的原因。
// 测试断言这种否定行为，以便在 MyGO 升级改变它时能察觉，而不是静默依赖该行为。
func TestButtonBaseWithOnlyALabelHasNoClickTarget(t *testing.T) {
	var clicks int
	view := func(c *ui.Context) {
		btn := ui.ButtonBase(c).Label("Bang")
		if btn.Clicked() {
			clicks++
		}
	}

	tr := ui.NewTester(view, 400, 200)
	tr.Frame()

	if err := tr.Click("Bang"); err != nil {
		// 连找到它都会失败，这是同一事实的另一面。
		t.Logf("the label alone is not clickable: %v", err)
		return
	}
	tr.Frame()
	if clicks != 0 {
		t.Fatalf("a label-only ButtonBase became clickable (%d clicks): "+
			"textButton's comment is now wrong and should be revisited", clicks)
	}
}

// 同一事实的正面情形：将标签作为子元素绘制的按钮会报告点击，这正是 textButton 所依赖的行为。
func TestTextButtonIsClickable(t *testing.T) {
	var clicks int
	view := func(c *ui.Context) {
		if textButton(c, "Bang").Clicked() {
			clicks++
		}
	}

	tr := ui.NewTester(view, 400, 200)
	tr.Frame()

	if err := tr.Click("Bang"); err != nil {
		t.Fatalf("a textButton is not clickable: %v", err)
	}
	tr.Frame()
	if clicks != 1 {
		t.Fatalf("clicks after clicking = %d, want 1", clicks)
	}
}
