package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ELY-Open-Source/CrychicUI/form"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

// editForm 是节点编辑对话框自身的状态，由根视图持有。
//
// 它有意设为视图本地状态：输入时不得访问服务，且只有该对话框读取这些字段。
// 离开此结构体的唯一内容，是保存时经过完整校验的 ProfileEdit。
type editForm struct {
	open bool

	id       string
	name     string
	address  string
	port     string // 文本类型，避免输入到一半的端口变成 0
	favorite bool

	// stored 是存储中的节点，用于校验：其中包含协议和出站 JSON，
	// 二者均不可编辑。
	stored store.Profile
	// problems 是本帧的 EditProblems 快照，用于渲染。
	problems []app.EditProblem
}

// load 从已存储的节点填充表单。
func (f *editForm) load(p store.Profile) {
	f.id = p.ID
	f.name = p.Name
	f.address = p.Address
	f.port = strconv.Itoa(p.Port)
	f.favorite = p.Favorite
	f.stored = p
	f.open = true
}

// edit 返回对话框的当前值。
func (f *editForm) edit() app.ProfileEdit {
	port, _ := strconv.Atoi(strings.TrimSpace(f.port))
	return app.ProfileEdit{
		ID:       f.id,
		Name:     f.name,
		Address:  f.address,
		Port:     port,
		Favorite: f.favorite,
	}
}

// problemFor 返回某个字段的错误消息，没有则返回 ""。
func (f *editForm) problemFor(field string) string {
	for _, p := range f.problems {
		if p.Field == field {
			return p.Message
		}
	}
	return ""
}

// openEdit 开始编辑指定 ID 的节点。
//
// 存储读取在点击时发生，而不是在每帧中进行：Profile() 属于 IO，
// 视图不得每帧执行 IO。
func (r *Root) openEdit(id string) {
	p, ok := r.svc.Profile(id)
	if !ok {
		return
	}
	r.editF.load(p)
}

// editDialog 在页面上方绘制节点编辑器。
//
// 任一字段无效时禁用保存，每条消息显示在其对应控件下方。缺少公钥的 REALITY
// 是值得单独说明的情况：节点无法连接，而公钥又不是用户能在这里输入的内容，
// 因此对话框会说明原因，而不是允许保存。
func (r *Root) editDialog(c *ui.Context) {
	f := &r.editF
	if !f.open {
		return
	}
	t := c.Theme()

	// 每帧根据当前输入重新计算，因此字段修正后错误会立即消失。
	f.problems = app.EditProblems(f.edit(), f.stored)

	r.modal(c, "编辑节点", &f.open, func() {
		ui.Column(c).FillWidth().Gap(t.Space(3)).Children(func() {
			ui.Text(c, fmt.Sprintf("%s · %s", f.stored.Protocol, f.stored.Name)).
				TextColor(t.TextMuted).FontSize(t.Rem(0.85))

			form.FieldGroup(c, tr(c, "名称"), form.FieldGroupOptions{Error: f.problemFor("name")}, func() {
				ui.TextInput(c, &f.name).FillWidth()
			})
			form.FieldGroup(c, tr(c, "地址"), form.FieldGroupOptions{Error: f.problemFor("address")}, func() {
				ui.TextInput(c, &f.address).FillWidth()
			})
			form.FieldGroup(c, tr(c, "端口"), form.FieldGroupOptions{Error: f.problemFor("port")}, func() {
				ui.TextInput(c, &f.port).FillWidth()
			})
			ui.Checkbox(c, &f.favorite, tr(c, "收藏"))

			// 在此处无法修复的那个字段也会显示，使保存按钮被禁用的原因
			// 始终可见。
			if msg := f.problemFor("publicKey"); msg != "" {
				ui.Text(c, msg).TextColor(t.Danger).FontSize(t.Rem(0.85))
			}

			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				if len(f.problems) > 0 {
					// 禁用的按钮仍会显示其原本用途；原因见上方红字。
					textButton(c, "保存", disabled())
				} else if textButton(c, "保存", primary()).Clicked() {
					r.svc.SaveProfile(f.edit())
					f.open = false
				}
				if textButton(c, "取消").Clicked() {
					f.open = false
				}
			})
		})
	})
}
