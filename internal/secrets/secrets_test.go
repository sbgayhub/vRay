package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

// fakePlatform 是内存中的 Platform，同时执行 mygo 的大小上限，因此超大值
// 路径会按真实存储的失败方式被测试到。
type fakePlatform struct {
	m map[string][]byte
}

var errTooBig = errors.New("mygo: secret has more bytes than MaxSecretSize")

func newFakePlatform() *fakePlatform { return &fakePlatform{m: map[string][]byte{}} }

// newSystemWith 基于任意 Platform 构建 System，供测试使用。
func newSystemWith(p Platform, service, path string) *System {
	return &System{platform: p, service: service, path: path}
}

func (p *fakePlatform) Set(key string, value []byte) error {
	if len(value) > MaxInline {
		return errTooBig
	}
	p.m[key] = append([]byte(nil), value...)
	return nil
}

func (p *fakePlatform) Get(key string) ([]byte, error) {
	v, ok := p.m[key]
	if !ok {
		// MyGO 的存储以这种方式报告键不存在，Get 依赖它继续回退到加密文件。
		return nil, mygo.ErrSecretNotFound
	}
	return v, nil
}

func (p *fakePlatform) Delete(key string) error {
	delete(p.m, key)
	return nil
}

func newTestKeeper(t *testing.T) (*System, *fakePlatform) {
	t.Helper()
	p := newFakePlatform()
	return newSystemWith(p, "dev.sbgayhub.vray", filepath.Join(t.TempDir(), "secrets.enc")), p
}

// 能容纳的值保存在平台存储中，并可读回。
func TestSmallSecretRoundTrip(t *testing.T) {
	k, p := newTestKeeper(t)
	want := []byte("11111111-2222-3333-4444-555555555555")
	if err := k.Set("node/uuid", want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, ok := p.m["node/uuid"]; !ok {
		t.Fatal("small secret did not go to the platform store")
	}
	got, err := k.Get("node/uuid")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// 超过上限的订阅 URL 会被加密写入文件，读回时仍保持完整。
func TestOversizedSecretFallsBackToSealedFile(t *testing.T) {
	k, p := newTestKeeper(t)
	long := []byte("https://sub.example.com/api/v1/client/subscribe?token=" + strings.Repeat("a", MaxInline))

	if err := k.Set("source/url", long); err != nil {
		t.Fatalf("Set oversized: %v", err)
	}
	if _, ok := p.m["source/url"]; ok {
		t.Fatal("oversized secret should not sit in the platform store")
	}
	got, err := k.Get("source/url")
	if err != nil {
		t.Fatalf("Get oversized: %v", err)
	}
	if !bytes.Equal(got, long) {
		t.Fatalf("oversized secret did not round-trip (%d bytes)", len(got))
	}
}

// 加密文件不包含明文。
func TestOversizedSecretIsNotPlainText(t *testing.T) {
	k, _ := newTestKeeper(t)
	secret := "token=" + strings.Repeat("Z", MaxInline)
	if err := k.Set("source/url", []byte(secret)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	raw, err := os.ReadFile(k.path)
	if err != nil {
		t.Fatalf("read sealed: %v", err)
	}
	if bytes.Contains(raw, []byte("ZZZZZZZZ")) {
		t.Fatal("sealed file contains the plain text")
	}
}

// 较大的值之后变小时，会移回平台存储，不留下过期的加密副本。
func TestShrinkingValueMovesBackInline(t *testing.T) {
	k, p := newTestKeeper(t)
	big := []byte(strings.Repeat("x", MaxInline+100))
	if err := k.Set("k", big); err != nil {
		t.Fatalf("Set big: %v", err)
	}
	small := []byte("short")
	if err := k.Set("k", small); err != nil {
		t.Fatalf("Set small: %v", err)
	}
	if _, ok := p.m["k"]; !ok {
		t.Fatal("small value did not return to the platform store")
	}
	got, err := k.Get("k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, small) {
		t.Fatalf("got %q, want %q", got, small)
	}
}

// Delete 会从两个存储中移除值，且不再有任何可读取的内容。
func TestDeleteRemovesBothStores(t *testing.T) {
	k, p := newTestKeeper(t)
	if err := k.Set("small", []byte("v")); err != nil {
		t.Fatalf("Set small: %v", err)
	}
	if err := k.Set("big", []byte(strings.Repeat("y", MaxInline+1))); err != nil {
		t.Fatalf("Set big: %v", err)
	}

	for _, key := range []string{"small", "big"} {
		if err := k.Delete(key); err != nil {
			t.Fatalf("Delete %s: %v", key, err)
		}
		if _, err := k.Get(key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s still readable after delete (err = %v)", key, err)
		}
	}
	if _, ok := p.m["small"]; ok {
		t.Fatal("small secret still in the platform store")
	}
}

// 未知键会报告 ErrNotFound。
func TestGetMissing(t *testing.T) {
	k, _ := newTestKeeper(t)
	if _, err := k.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// 同一值加密两次会生成不同密文（随机 nonce）。
func TestSealUsesFreshNonce(t *testing.T) {
	k, _ := newTestKeeper(t)
	value := []byte("same-value")
	a, err := k.seal(value)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	b, err := k.seal(value)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same value are identical; nonce is reused")
	}
}
