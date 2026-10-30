package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/sbgayhub/vray/internal/diag"
)

// DiagnosticReport 从应用已持有的状态中组装缺陷报告所需的信息。
//
// 它读取存储以生成节点与订阅摘要，但绝不接触凭据保管器：能触达凭据的
// 诊断包离泄漏只差一个缺陷，因此干脆不提供这项能力。
func (s *Service) DiagnosticReport() diag.Report {
	snap := s.state.Snapshot()
	r := diag.Report{
		Memory:   diag.ReadMemory(),
		Created:  time.Now(),
		App:      Version,
		Platform: fmt.Sprintf("%s/%s %s", runtime.GOOS, runtime.GOARCH, runtime.Version()),
		Phase:    string(snap.Phase),
		Mode:     snap.Mode,
		GeoState: snap.GeoState,
	}
	if snap.Problem != nil {
		r.Problem = &diag.Problem{
			Code:    snap.Problem.Code,
			Message: snap.Problem.Message,
			Detail:  snap.Problem.Detail,
		}
	}

	// 节点摘要：协议、脱敏地址、端口、安全层。只从出站 JSON 读取安全字段，
	// 它解释了大多数“连不上”的报告，且不属于机密。
	if d, err := s.store.Load(); err == nil {
		for _, p := range d.Profiles {
			var o struct {
				Security string `json:"security"`
			}
			if len(p.RawOutbound) > 0 {
				_ = json.Unmarshal(p.RawOutbound, &o)
			}
			source := "link"
			if p.SourceID != "" {
				source = "subscription"
			}
			r.Nodes = append(r.Nodes, diag.NodeSummary{
				Protocol: string(p.Protocol),
				Address:  p.Address,
				Port:     p.Port,
				Security: o.Security,
				Source:   source,
			})
		}
		for _, src := range d.Sources {
			n := 0
			for _, p := range d.Profiles {
				if p.SourceID == src.ID {
					n++
				}
			}
			r.Sources = append(r.Sources, diag.SourceSummary{
				Name:    src.Name,
				Enabled: src.Enabled,
				// 这里从不读取 URL：URL 保存在凭据保管器中，本函数不得
				// 接触它。脱敏器最终会替换这个占位符。
				URL:       "set",
				LastErr:   src.LastError,
				NodeCount: n,
			})
		}
	}

	for _, c := range s.SelfCheck() {
		r.Checks = append(r.Checks, diag.Check{
			Name: c.Name, State: string(c.State), Detail: c.Detail,
		})
	}

	if entries := s.Logs(); len(entries) > 0 {
		// 只取末尾：一万行的包不是缺陷报告，而是没人会读的附件。
		const maxLines = 200
		if len(entries) > maxLines {
			entries = entries[len(entries)-maxLines:]
		}
		for _, e := range entries {
			r.Logs = append(r.Logs, diag.LogLine{
				Level: e.Level.String(),
				Time:  e.At.Format(time.RFC3339),
				Text:  e.Text,
			})
		}
	}

	r.Files = listDataFiles(s.paths.Root)
	return r
}

// Version 是诊断包报告的应用版本。
//
// 使用变量而不是常量，发布构建可以通过 -ldflags 设置；默认值与 mygo.json 一致。
var Version = "0.2.1"

// listDataFiles 列出数据目录中的内容。
//
// 只列名称和大小：诊断包不得携带保存全部节点的数据库或凭据文件。
func listDataFiles(root string) []diag.FileInfo {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []diag.FileInfo
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		if e.IsDir() {
			// 目录只列名称以表明存在，但不遍历内容：递归列出数据目录
			// 等于绘制用户订阅的地图。
			out = append(out, diag.FileInfo{Name: name + "/", Size: 0})
			continue
		}
		out = append(out, diag.FileInfo{Name: name, Size: info.Size()})
	}
	return out
}

// ExportDiagnostics 把诊断包写入数据目录并返回其路径。
//
// 文件写入数据目录而不是临时目录：用户需要找到它才能附到报告里，
// 而数据目录正是已经告知用户的位置。
func (s *Service) ExportDiagnostics() (string, error) {
	r := s.DiagnosticReport()
	raw, err := diag.Bytes(r)
	if err != nil {
		return "", fmt.Errorf("生成诊断包失败：%w", err)
	}
	name := fmt.Sprintf("vray-diagnostics-%s.zip", time.Now().Format("20060102-150405"))
	path := filepath.Join(s.paths.Root, name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("写入诊断包失败：%w", err)
	}
	return path, nil
}

// ExportDiagnosticsAsync 是日志页使用的非阻塞入口。
func (s *Service) ExportDiagnosticsAsync() <-chan struct{} {
	done := make(chan struct{})
	s.state.Update(func(sn *Snapshot) {
		sn.DiagnosticSaving = true
		sn.DiagnosticResult = ""
	})
	go func() {
		defer close(done)
		path, err := s.ExportDiagnostics()
		message := "已导出到 " + path
		if err != nil {
			message = "导出失败：" + err.Error()
		}
		s.state.Update(func(sn *Snapshot) {
			sn.DiagnosticSaving = false
			sn.DiagnosticResult = message
		})
	}()
	return done
}
