package app

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/sbgayhub/vray/internal/paths"
	"github.com/sbgayhub/vray/internal/secrets"
	"github.com/sbgayhub/vray/internal/store"
)

// FakeEngine 在测试中代替内核：记录收到的内容，并可被要求失败。
type FakeEngine struct {
	mu       sync.Mutex
	cfg      []byte
	running  bool
	startErr error
	starts   int
	stops    int
}

// Start 记录配置。
func (f *FakeEngine) Start(cfg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.cfg = append([]byte(nil), cfg...)
	f.running = true
	f.starts++
	return nil
}

// Stop 清除运行标记。
func (f *FakeEngine) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
	f.stops++
	return nil
}

// Running 返回 Start 是否成功且 Stop 尚未执行。
func (f *FakeEngine) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

// Config 返回最近一次传给 Start 的配置。
func (f *FakeEngine) Config() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.cfg...)
}

// Counts 返回启动与停止的次数。
func (f *FakeEngine) Counts() (starts, stops int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops
}

// FailStart 让下一次 Start 失败。
func (f *FakeEngine) FailStart(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startErr = err
}

// MemSecrets 是内存中的 secrets.Keeper。
type MemSecrets struct {
	mu sync.Mutex
	m  map[string][]byte
}

// NewMemSecrets 返回空的保管器。
func NewMemSecrets() *MemSecrets { return &MemSecrets{m: map[string][]byte{}} }

// Set 保存一个值。
func (m *MemSecrets) Set(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[key] = append([]byte(nil), value...)
	return nil
}

// Get 读取一个值。
func (m *MemSecrets) Get(key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.m[key]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	return v, nil
}

// Delete 删除一个值。
func (m *MemSecrets) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, key)
	return nil
}

// NewFake 在内存依赖之上构建 Service，供测试与 UI 测试使用。
//
// 数据库写入调用方提供的目录，保证测试相互隔离。
func NewFake(dir string) (*Service, *FakeEngine) {
	p := paths.Paths{
		Root:         dir,
		DefaultRoot:  dir,
		LocationFile: filepath.Join(dir, paths.LocationName),
		Mode:         paths.ModeDefault,
	}
	st := NewState(Snapshot{Phase: PhaseIdle, Mode: store.DefaultSettings().Mode})
	engine := &FakeEngine{}
	storer := store.NewFile(p.DB())
	// 大多数测试验证引导完成后的应用。首次运行协议相关测试会自行构建
	// 全新的 Service 与存储。
	if d, err := storer.Load(); err == nil {
		d.Settings.TermsAcceptedVersion = store.LegalVersion
		d.Settings.TermsAcceptedAt = time.Now()
		_ = storer.Save(d)
	}
	svc := New(st, storer, NewMemSecrets(), engine, p)
	// 假内核不启动 API 入站，真实统计客户端会在每次连接测试中花三秒
	// 拨一个无人监听的端口。需要流量的测试用 AttachStats 注入自己的源。
	svc.noRealStats = true
	svc.noResolve = true
	return svc, engine
}

// AddTestProfile 直接存储一个节点，供需要预置节点的测试使用。
func (s *Service) AddTestProfile(p store.Profile, secret string) error {
	if secret != "" {
		key := "profile/" + p.ID + "/secret"
		if err := s.secrets.Set(key, []byte(secret)); err != nil {
			return err
		}
		p.SecretRef = key
	}
	if p.ID == "" {
		p.ID = newID()
	}
	return s.store.PutProfile(p)
}

// SetModeForTest 直接写入模式，不执行重连流程。
func (s *Service) SetModeForTest(mode string) error {
	d, err := s.store.Load()
	if err != nil {
		return err
	}
	d.Settings.Mode = mode
	if err := s.store.SetSettings(d.Settings); err != nil {
		return err
	}
	s.state.Update(func(sn *Snapshot) { sn.Mode = mode })
	return nil
}

// SetRulesForTest 绕过校验，把规则直接写入存储。
//
// 服务有意拒绝畸形规则，因此需要验证页面如何处理“文件中已存在”的坏规则
// 时，测试只能通过另一条路径写入。
func (s *Service) SetRulesForTest(rules []store.Rule) error {
	if err := s.store.SetRules(rules); err != nil {
		return err
	}
	return s.Load()
}
