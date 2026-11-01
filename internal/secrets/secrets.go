// secrets 包负责保存节点凭据和订阅 URL。
//
// MyGO 的 Secrets 使用系统存储（Keychain、Secret Service、Credential Manager），
// 每个值上限为 2560 字节（mygo.MaxSecretSize）。订阅 URL 和 ss 密码可能超过该上限，
// 因此过大的值会改为加密后写入存储旁的文件。
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
)

// MaxInline 是系统存储可保存的最大值，来自 MyGO。
const MaxInline = mygo.MaxSecretSize

// ErrNotFound 表示某个键没有值。
var ErrNotFound = errors.New("凭据：未找到")

// Keeper 存储机密数据。System 基于 mygo.Secrets 实现，并为放不下的值使用文件。
type Keeper interface {
	Set(key string, value []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

// Platform 是 Keeper 背后的系统存储；生产环境使用 mygo.Secrets，测试中使用 map，
// 因此无需运行应用即可测试超大值路径。
type Platform interface {
	Set(key string, value []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

// mygoPlatform 是面向单个服务的、基于 mygo.Secrets 的 Platform。
type mygoPlatform struct{ service string }

func (p mygoPlatform) store() mygo.SecretsModule { return mygo.Secrets.Service(p.service) }

func (p mygoPlatform) Set(key string, value []byte) error { return p.store().Set(key, value) }
func (p mygoPlatform) Get(key string) ([]byte, error)     { return p.store().Get(key) }
func (p mygoPlatform) Delete(key string) error            { return p.store().Delete(key) }

// System 是生产环境使用的 Keeper。
//
// 长度不超过 MaxInline 的键值存入平台存储。较大的值使用平台存储中的密钥以 AES-GCM 加密，
// 并存储在应用数据旁的文件中。
type System struct {
	platform Platform
	service  string // 用于命名存储并派生回退密钥
	path     string // 用于存放超大值加密数据的文件
	mu       sync.Mutex
	key      []byte // 缓存的 AES 密钥；惰性生成，受 mu 保护
}

// NewSystem 返回一个 Keeper：使用 service 下的 mygo.Secrets，并使用 path 处的文件存放超大值。
func NewSystem(service, path string) *System {
	return &System{platform: mygoPlatform{service: service}, service: service, path: path}
}

// Set 在 key 下存储 value。
func (s *System) Set(key string, value []byte) error {
	if len(value) <= MaxInline {
		if err := s.platform.Set(key, value); err != nil {
			return fmt.Errorf("凭据：在系统凭据库中设置 %q 失败：%w", key, err)
		}
		// 较小的值会替换先前较大的值。
		return s.removeSealed(key)
	}
	// 密钥的生成与值的密封必须整体持有 mu：两个大值并发 Set 时若各自
	// 生成随机密钥，平台存储只保留后写的一把，先 seal 的值将永久无法解密。
	s.mu.Lock()
	defer s.mu.Unlock()
	sealed, err := s.seal(value)
	if err != nil {
		return err
	}
	m, err := s.readSealed()
	if err != nil {
		return err
	}
	m[key] = base64.StdEncoding.EncodeToString(sealed)
	return s.writeSealed(m)
}

// Get 读取 key 的值。
func (s *System) Get(key string) ([]byte, error) {
	if v, err := s.platform.Get(key); err == nil {
		return v, nil
	} else if !errors.Is(err, mygo.ErrSecretNotFound) && !errors.Is(err, errors.ErrUnsupported) {
		return nil, fmt.Errorf("凭据：读取 %q 失败：%w", key, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readSealed()
	if err != nil {
		return nil, err
	}
	enc, ok := m[key]
	if !ok {
		return nil, ErrNotFound
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("凭据：解码密封值 %q 失败：%w", key, err)
	}
	return s.open(raw)
}

// Delete 从两个存储中删除 key。
func (s *System) Delete(key string) error {
	if err := s.platform.Delete(key); err != nil && !errors.Is(err, mygo.ErrSecretNotFound) && !errors.Is(err, errors.ErrUnsupported) {
		return fmt.Errorf("凭据：删除 %q 失败：%w", key, err)
	}
	return s.removeSealed(key)
}

// removeSealed 从加密文件中删除 key，文件不存在时忽略。
func (s *System) removeSealed(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readSealed()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return nil
	}
	delete(m, key)
	return s.writeSealed(m)
}

// sealKey 返回 AES-256 密钥。首次调用在平台存储中创建它并缓存，之后直接
// 复用，避免每次加解密都往返平台凭据库。调用方必须持有 s.mu。
func (s *System) sealKey() ([]byte, error) {
	if len(s.key) == 32 {
		return s.key, nil
	}
	const name = "secrets.enc.key"
	if k, err := s.platform.Get(name); err == nil && len(k) == 32 {
		s.key = k
		return k, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("凭据：生成随机密钥失败：%w", err)
	}
	if err := s.platform.Set(name, key); err != nil {
		// 没有平台存储时，没有可用于保护密钥的机制；回退为根据文件路径派生的密钥，
		// 以免数据以明文存储。该密钥可由文件路径推导，只提供混淆而非保护，
		// 必须让用户在日志里看到这次安全降级。
		slog.Warn("凭据：系统凭据库不可用，超大值回退为路径派生密钥（仅混淆，非安全保护）", "错误", err)
		sum := sha256.Sum256([]byte(s.service + "|" + s.path))
		key = sum[:]
	}
	s.key = key
	return key, nil
}

// seal 用 AES-GCM 密封 plain。调用方必须持有 s.mu。
func (s *System) seal(plain []byte) ([]byte, error) {
	key, err := s.sealKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("凭据：创建加密器失败：%w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("凭据：创建 GCM 失败：%w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("凭据：生成随机数失败：%w", err)
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

// open 打开密封值。调用方必须持有 s.mu。
func (s *System) open(sealed []byte) ([]byte, error) {
	key, err := s.sealKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("凭据：创建解密器失败：%w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("凭据：创建 GCM 失败：%w", err)
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("凭据：密封值已被截断")
	}
	nonce, body := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("凭据：打开失败：%w", err)
	}
	return plain, nil
}

// readSealed 加载超大值映射；文件不存在时返回空映射。
func (s *System) readSealed() (map[string]string, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("凭据：读取 %s 失败：%w", s.path, err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("凭据：解析 %s 失败：%w", s.path, err)
	}
	return m, nil
}

func (s *System) writeSealed(m map[string]string) error {
	if len(m) == 0 {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("凭据：删除 %s 失败：%w", s.path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("凭据：创建目录失败：%w", err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("凭据：编码失败：%w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".secrets-*.json")
	if err != nil {
		return fmt.Errorf("凭据：创建临时文件失败：%w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("凭据：写入失败：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("凭据：关闭失败：%w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("凭据：重命名失败：%w", err)
	}
	return nil
}
