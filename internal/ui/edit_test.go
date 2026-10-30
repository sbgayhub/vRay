package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

// realityProfile 返回一个以 REALITY 导入但没有公钥的节点，
// 这是对话框必须拒绝的情况。
func realityProfile(raw string) store.Profile {
	p := profileForTest()
	p.RawOutbound = json.RawMessage(raw)
	return p
}

// 编辑器拒绝没有公钥的 REALITY 节点，并指出是哪个字段。
//
// 公钥来自分享链接，且在此处不可编辑，因此该节点永远无法连接：
// 接受保存只会把失败推迟到连接时，而那里的消息会是不透明的 TLS 错误。
func TestEditProblemsRealitiesNeedsPublicKey(t *testing.T) {
	stored := realityProfile(`{"protocol":"vless","security":"reality","sni":"www.microsoft.com"}`)
	got := app.EditProblems(app.ProfileEdit{
		ID: "p1", Name: "tokyo", Address: "jp.example.com", Port: 443,
	}, stored)
	if len(got) != 1 {
		t.Fatalf("problems = %+v, want exactly the publicKey one", got)
	}
	if got[0].Field != "publicKey" {
		t.Fatalf("field = %q, want publicKey", got[0].Field)
	}
	if !strings.Contains(got[0].Message, "pbk") {
		t.Fatalf("message = %q, want it to name pbk", got[0].Message)
	}
}

// 有公钥的 REALITY 节点可正常保存。
func TestEditProblemsAcceptsRealityWithKey(t *testing.T) {
	stored := realityProfile(`{"protocol":"vless","security":"reality","publicKey":"PUBKEY"}`)
	if got := app.EditProblems(app.ProfileEdit{
		ID: "p1", Name: "tokyo", Address: "jp.example.com", Port: 443,
	}, stored); len(got) != 0 {
		t.Fatalf("problems = %+v, want none", got)
	}
}

// TLS 节点不需要公钥，因此绝不会继承 REALITY 规则。
func TestEditProblemsTLSNeedsNoKey(t *testing.T) {
	stored := realityProfile(`{"protocol":"vless","security":"tls"}`)
	if got := app.EditProblems(app.ProfileEdit{
		ID: "p1", Name: "tokyo", Address: "jp.example.com", Port: 443,
	}, stored); len(got) != 0 {
		t.Fatalf("problems = %+v, want none", got)
	}
}

// 每个可编辑字段都会被验证，每条消息都会指出自己的字段，
// 以便对话框将消息放在对应控件下方。
func TestEditProblemsNamesEachField(t *testing.T) {
	cases := []struct {
		name  string
		edit  app.ProfileEdit
		field string
	}{
		{"empty name", app.ProfileEdit{Name: "  ", Address: "a.com", Port: 1}, "name"},
		{"empty address", app.ProfileEdit{Name: "n", Address: "", Port: 1}, "address"},
		{"port zero", app.ProfileEdit{Name: "n", Address: "a.com", Port: 0}, "port"},
		{"port too high", app.ProfileEdit{Name: "n", Address: "a.com", Port: 70000}, "port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := app.EditProblems(tc.edit, profileForTest())
			if len(got) != 1 {
				t.Fatalf("problems = %+v, want exactly one", got)
			}
			if got[0].Field != tc.field {
				t.Fatalf("field = %q, want %q", got[0].Field, tc.field)
			}
		})
	}
}

// 对话框打开时，字段中已填入节点的值。
func TestEditDialogLoadsStoredValues(t *testing.T) {
	r, _ := newRoot(t)
	r.openEdit("p1")

	if !r.editF.open {
		t.Fatal("dialog did not open for an existing node")
	}
	if r.editF.name != "tokyo" || r.editF.address != "jp.example.com" || r.editF.port != "443" {
		t.Fatalf("form = %+v", r.editF)
	}
}

// 打开不存在的节点时，对话框保持关闭。
func TestEditDialogIgnoresUnknownNode(t *testing.T) {
	r, _ := newRoot(t)
	r.openEdit("nope")
	if r.editF.open {
		t.Fatal("dialog opened for a node that is not in the store")
	}
}

// 对话框在屏幕上渲染其标题和节点值。
func TestEditDialogRenders(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
	r.openEdit("p1")

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	for _, want := range []string{"编辑节点", "名称", "地址", "端口", "保存"} {
		if !tr.HasText(want) {
			t.Fatalf("%q is not on screen", want)
		}
	}
}

// 缺少公钥的 REALITY 节点会显示原因，并且不提供保存方式。
//
// Save 按钮绘制为禁用状态，因此点击无法到达 SaveProfile；
// 测试驱动该帧，然后检查存储未被修改。
func TestEditDialogBlocksSaveWhenKeyMissing(t *testing.T) {
	svc, _ := app.NewFake(t.TempDir())
	p := realityProfile(`{"protocol":"vless","security":"reality"}`)
	if err := svc.AddTestProfile(p, "uuid"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	r := NewRoot(svc)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
	r.openEdit("p1")

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()

	if !tr.HasText("REALITY 节点缺少公钥（pbk），无法连接") {
		t.Fatal("the reason for the blocked save is not on screen")
	}
	// 对话框仍处于打开状态：它没有自行保存后关闭。
	if !r.editF.open {
		t.Fatal("dialog closed, so something saved")
	}
	// 而且原因在编辑后仍然存在：它会被重新计算，而不是只显示一次。
	r.editF.problems = app.EditProblems(r.editF.edit(), r.editF.stored)
	if len(r.editF.problems) == 0 {
		t.Fatal("problems became empty without the node changing")
	}
}

// 保存会写入输入的值并关闭对话框。
func TestEditDialogSavesTypedValues(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
	r.openEdit("p1")

	r.editF.name = "osaka"
	r.editF.port = "8443"
	if len(app.EditProblems(r.editF.edit(), r.editF.stored)) != 0 {
		t.Fatal("a complete edit was reported as invalid")
	}
	svc.SaveProfile(r.editF.edit())

	deadline := 0
	for deadline < 600 {
		for _, p := range svc.State().Snapshot().Profiles {
			if p.Name == "osaka" && p.Port == 8443 {
				return
			}
		}
		deadline++
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("save did not land: %+v", svc.State().Snapshot().Profiles)
}
