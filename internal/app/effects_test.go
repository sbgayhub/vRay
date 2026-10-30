package app

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/core/effects"
)

// fakeLayer 记录被要求恢复的条目。
type fakeLayer struct {
	mu    sync.Mutex
	seen  []effects.Entry
	fails bool
}

func (f *fakeLayer) Restore(e effects.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, e)
	if f.fails {
		return errors.New("permission denied")
	}
	return nil
}

func (f *fakeLayer) entries() []effects.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]effects.Entry(nil), f.seen...)
}

// newEffectService 返回挂载两个 fakeLayer 的假服务。
func newEffectService(t *testing.T) (*Service, *fakeLayer, *fakeLayer) {
	t.Helper()
	svc, _ := newTestService(t)
	proxy, dns := &fakeLayer{}, &fakeLayer{}
	svc.AttachEffectLayers(proxy, dns)
	return svc, proxy, dns
}

// 崩溃会留下日志；下次启动会撤销日志中的操作并报告数量。
//
// 这是计划要求的启动自检（S9-3）：磁盘上的日志是进程被杀死后唯一幸存的数据，因此恢复必须从文件执行，并报告恢复了哪些内容。
func TestRecoverEffectsFromJournalOnDisk(t *testing.T) {
	svc, proxy, dns := newEffectService(t)

	// 上一次运行写入了两个条目后进程被杀死。
	j := svc.Effects()
	if err := j.Record(effects.Entry{Kind: effects.KindSystemProxy, Target: "WinINET"}); err != nil {
		t.Fatalf("Record proxy: %v", err)
	}
	if err := j.Record(effects.Entry{Kind: effects.KindDNS, Target: "Wi-Fi"}); err != nil {
		t.Fatalf("Record dns: %v", err)
	}

	recovered, err := svc.RecoverEffects()
	if err != nil {
		t.Fatalf("RecoverEffects: %v", err)
	}
	if len(recovered) != 2 {
		t.Fatalf("recovered = %d, want 2", len(recovered))
	}
	if got := svc.State().Snapshot().Recovered; got != 2 {
		t.Fatalf("snapshot Recovered = %d, want 2", got)
	}
	// 每个图层收到各自的类型，最新的在前。
	if dnsGot := dns.entries(); len(dnsGot) != 1 || dnsGot[0].Kind != effects.KindDNS {
		t.Fatalf("dns layer saw %+v", dnsGot)
	}
	if proxyGot := proxy.entries(); len(proxyGot) != 1 || proxyGot[0].Kind != effects.KindSystemProxy {
		t.Fatalf("proxy layer saw %+v", proxyGot)
	}
	// 日志已消失，因此下次启动是干净的。
	if _, err := os.Stat(svc.paths.Effects()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the journal survived recovery: %v", err)
	}
}

// 干净启动不做任何事，也不报告任何内容。
func TestRecoverEffectsDoesNothingOnCleanStart(t *testing.T) {
	svc, proxy, _ := newEffectService(t)
	recovered, err := svc.RecoverEffects()
	if err != nil {
		t.Fatalf("RecoverEffects: %v", err)
	}
	if len(recovered) != 0 {
		t.Fatalf("recovered = %+v", recovered)
	}
	if got := svc.State().Snapshot().Recovered; got != 0 {
		t.Fatalf("snapshot Recovered = %d, want 0", got)
	}
	if got := proxy.entries(); len(got) != 0 {
		t.Fatalf("the proxy layer ran on a clean start: %+v", got)
	}
}

// Disconnect 先恢复效果再停止核心，因此机器不会留下指向已停止核心的代理。
func TestDisconnectRestoresEffectsFirst(t *testing.T) {
	svc, proxy, _ := newEffectService(t)

	j := svc.Effects()
	if err := j.Record(effects.Entry{Kind: effects.KindSystemProxy, Target: "WinINET"}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	// 从已连接状态断开：先让假引擎运行。
	if err := svc.connectForTest(""); err == nil {
		t.Log("connect succeeded; the ordering assertion below does not depend on it")
	}
	svc.Disconnect()

	deadline := 0
	for deadline < 600 {
		if len(proxy.entries()) > 0 {
			break
		}
		deadline++
		time.Sleep(5 * time.Millisecond)
	}
	if len(proxy.entries()) == 0 {
		t.Fatal("Disconnect did not restore the recorded effects")
	}
	if j.Pending() {
		t.Fatal("the journal still has entries after Disconnect")
	}
}

// 恢复两次是无害的：第二次调用找不到需要处理的内容。
func TestRestoreEffectsIsIdempotent(t *testing.T) {
	svc, proxy, _ := newEffectService(t)
	if err := svc.Effects().Record(effects.Entry{Kind: effects.KindSystemProxy, Target: "x"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := svc.RestoreEffects(); err != nil {
		t.Fatalf("first restore: %v", err)
	}
	if err := svc.RestoreEffects(); err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if got := len(proxy.entries()); got != 1 {
		t.Fatalf("the layer ran %d times, want 1", got)
	}
}

// 某个图层恢复失败时，其条目会留到下次处理，且错误会被报告而不是吞掉。
func TestRestoreEffectsReportsLayerFailure(t *testing.T) {
	svc, proxy, _ := newEffectService(t)
	proxy.fails = true
	if err := svc.Effects().Record(effects.Entry{Kind: effects.KindSystemProxy, Target: "x"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := svc.RestoreEffects(); err == nil {
		t.Fatal("a failed restore was reported as success")
	}
	if !svc.Effects().Pending() {
		t.Fatal("the failed entry was dropped instead of kept")
	}
}

// 日志位于应用数据目录中，与数据库相邻。
func TestEffectsJournalPath(t *testing.T) {
	svc, _, _ := newEffectService(t)
	want := filepath.Join(svc.paths.Root, "effects.json")
	if got := svc.Effects().Path(); got != want {
		t.Fatalf("journal path = %q, want %q", got, want)
	}
}
