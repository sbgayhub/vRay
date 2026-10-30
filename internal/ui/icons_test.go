package ui

import (
	"testing"

	"github.com/ELY-Open-Source/CrychicUI/icon"
)

// 节点页和订阅页使用的图标必须存在；否则 Must 会 panic，并导致帧崩溃。
func TestUsedIconsExist(t *testing.T) {
	for _, name := range []string{"check", "download", "search", "server", "shield", "house", "git-branch", "settings", "scroll-text"} {
		if icon.Must(name) == nil {
			t.Fatalf("icon %q is nil", name)
		}
	}
}
